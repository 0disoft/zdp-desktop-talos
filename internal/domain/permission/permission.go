package permission

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

var ErrInvalidRecord = errors.New("invalid permission record")

type Outcome string

const (
	OutcomeDeny           Outcome = "deny"
	OutcomeRequireReview  Outcome = "require_review"
	OutcomeAllowOnce      Outcome = "allow_once"
	OutcomeAllowTask      Outcome = "allow_task"
	OutcomeAllowWorkspace Outcome = "allow_workspace"
)

type GrantState string

const (
	GrantActive   GrantState = "active"
	GrantConsumed GrantState = "consumed"
	GrantRevoked  GrantState = "revoked"
)

type ProcessIntent struct {
	TaskID           string
	WorkspaceRoot    string
	RuleID           string
	Executable       string
	Arguments        []string
	EnvironmentNames []string
	Timeout          time.Duration
	MaxOutputBytes   int
}

type ProcessCapability struct {
	ID               string
	Executable       string
	ArgumentPrefix   []string
	MaxArguments     int
	EnvironmentNames []string
	MaxTimeout       time.Duration
	MaxOutputBytes   int
	IntentHash       string
}

type Grant struct {
	ID             string
	Outcome        Outcome
	State          GrantState
	CapabilityHash string
	TaskID         string
	WorkspaceHash  string
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

func (i ProcessIntent) Validate() error {
	if i.TaskID == "" || i.RuleID == "" || !filepath.IsAbs(i.WorkspaceRoot) || !filepath.IsAbs(i.Executable) || len(i.Arguments) > 256 || len(i.EnvironmentNames) > 32 || i.Timeout <= 0 || i.MaxOutputBytes <= 0 {
		return ErrInvalidRecord
	}
	for _, value := range i.Arguments {
		if value == "" || strings.IndexByte(value, 0) >= 0 || len(value) > 32767 {
			return ErrInvalidRecord
		}
	}
	seenEnvironment := make(map[string]struct{}, len(i.EnvironmentNames))
	for _, name := range i.EnvironmentNames {
		key := environmentKey(name)
		if !validEnvironmentName(name) {
			return ErrInvalidRecord
		}
		if _, exists := seenEnvironment[key]; exists {
			return ErrInvalidRecord
		}
		seenEnvironment[key] = struct{}{}
	}
	return nil
}

func (g Grant) Validate() error {
	if g.ID == "" || len(g.ID) > 128 || !validHash(g.CapabilityHash) || !validHash(g.WorkspaceHash) || g.CreatedAt.IsZero() || (g.State != GrantActive && g.State != GrantConsumed && g.State != GrantRevoked) {
		return ErrInvalidRecord
	}
	switch g.Outcome {
	case OutcomeAllowOnce:
		if g.TaskID == "" || g.WorkspaceHash == "" {
			return ErrInvalidRecord
		}
	case OutcomeAllowTask:
		if g.TaskID == "" || g.WorkspaceHash == "" {
			return ErrInvalidRecord
		}
	case OutcomeAllowWorkspace:
		if g.TaskID != "" || g.WorkspaceHash == "" {
			return ErrInvalidRecord
		}
	case OutcomeDeny:
		if g.WorkspaceHash == "" {
			return ErrInvalidRecord
		}
	default:
		return ErrInvalidRecord
	}
	if !g.ExpiresAt.IsZero() && !g.ExpiresAt.After(g.CreatedAt) {
		return ErrInvalidRecord
	}
	return nil
}

func IntentHash(intent ProcessIntent) (string, error) {
	return hashIntent(intent, true)
}

func WorkspaceIntentHash(intent ProcessIntent) (string, error) {
	return hashIntent(intent, false)
}

func hashIntent(intent ProcessIntent, includeTask bool) (string, error) {
	if err := intent.Validate(); err != nil {
		return "", err
	}
	taskID := intent.TaskID
	if !includeTask {
		taskID = ""
	}
	normalized := struct {
		TaskID, WorkspaceRoot, RuleID, Executable string
		Arguments, EnvironmentNames               []string
		TimeoutMS                                 int64
		MaxOutputBytes                            int
	}{taskID, canonicalPathText(intent.WorkspaceRoot), intent.RuleID, canonicalPathText(intent.Executable), append([]string(nil), intent.Arguments...), canonicalEnvironmentNames(intent.EnvironmentNames), intent.Timeout.Milliseconds(), intent.MaxOutputBytes}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("hash permission intent: %w", err)
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

func WorkspaceHash(root string) (string, error) {
	if !filepath.IsAbs(root) || strings.IndexByte(root, 0) >= 0 {
		return "", ErrInvalidRecord
	}
	hash := sha256.Sum256([]byte(canonicalPathText(root)))
	return hex.EncodeToString(hash[:]), nil
}

func canonicalPathText(value string) string {
	value = filepath.Clean(value)
	if runtime.GOOS == "windows" {
		value = strings.ToLower(value)
	}
	return value
}

func canonicalEnvironmentNames(names []string) []string {
	canonical := make([]string, len(names))
	for index, name := range names {
		canonical[index] = environmentKey(name)
	}
	sort.Strings(canonical)
	return canonical
}

func environmentKey(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

func validEnvironmentName(name string) bool {
	if name == "" || len(name) > 128 || strings.HasPrefix(name, "=") {
		return false
	}
	for _, character := range name {
		if !(character == '_' || character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9') {
			return false
		}
	}
	return true
}

func validHash(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
