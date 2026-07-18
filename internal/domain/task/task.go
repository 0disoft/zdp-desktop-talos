package task

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	MaxGoalLength                 = 4096
	MaxScopeEntries               = 256
	MaxAcceptanceItems            = 128
	MaxContractTextSize           = 4096
	MaxVerificationCommands       = 32
	MaxVerificationArguments      = 64
	MaxVerificationArgumentLength = 32767
)

var (
	ErrInvalidRecord        = errors.New("invalid task record")
	commitPattern           = regexp.MustCompile(`^[0-9a-f]{40,64}$`)
	workspaceIDPattern      = regexp.MustCompile(`^workspace-v1-[0-9a-f]{64}$`)
	verificationRulePattern = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)
)

type Status string

const (
	StatusContracted Status = "contracted"
	StatusCompleted  Status = "completed"
	StatusDiscarded  Status = "discarded"
)

type Risk string

const (
	RiskLow    Risk = "low"
	RiskMedium Risk = "medium"
	RiskHigh   Risk = "high"
)

type Record struct {
	ID              string
	VaultID         string
	WorkspaceID     string
	WorkspaceRoot   string
	BaselineCommit  string
	Status          Status
	CurrentRevision int
	CreatedAt       time.Time
	UpdatedAt       time.Time
	LastEventID     string
}

type ContractRevision struct {
	TaskID               string
	Revision             int
	BaselineCommit       string
	Goal                 string
	AllowedPaths         []string
	ForbiddenActions     []string
	AcceptanceCriteria   []string
	VerificationCommands []VerificationCommand
	Risk                 Risk
	CreatedAt            time.Time
	EventID              string
}

type VerificationCommand struct {
	RuleID           string
	Arguments        []string
	WorkingDirectory string
}

func (c VerificationCommand) Normalize() (VerificationCommand, error) {
	workingDirectory, err := normalizeWorkingDirectory(c.WorkingDirectory)
	if err != nil {
		return VerificationCommand{}, err
	}
	normalized := VerificationCommand{RuleID: c.RuleID, Arguments: append([]string(nil), c.Arguments...), WorkingDirectory: workingDirectory}
	if err := validateVerificationCommands([]VerificationCommand{normalized}); err != nil {
		return VerificationCommand{}, err
	}
	return normalized, nil
}

func (r Record) Validate() error {
	if r.ID == "" || r.VaultID == "" || (r.WorkspaceID != "" && !workspaceIDPattern.MatchString(r.WorkspaceID)) || !filepath.IsAbs(r.WorkspaceRoot) || !commitPattern.MatchString(r.BaselineCommit) {
		return fmt.Errorf("%w: identity, workspace root, and baseline are required", ErrInvalidRecord)
	}
	if (r.Status != StatusContracted && r.Status != StatusCompleted && r.Status != StatusDiscarded) || r.CurrentRevision < 1 || r.CreatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) || r.LastEventID == "" {
		return fmt.Errorf("%w: lifecycle metadata is invalid", ErrInvalidRecord)
	}
	return nil
}

func (c ContractRevision) AllowsPath(candidate string) bool {
	candidate = path.Clean(strings.ReplaceAll(candidate, "\\", "/"))
	if candidate == "." || candidate == ".." || strings.HasPrefix(candidate, "../") || strings.HasPrefix(candidate, "/") {
		return false
	}
	for _, pattern := range c.AllowedPaths {
		pattern = path.Clean(strings.ReplaceAll(pattern, "\\", "/"))
		if strings.HasSuffix(pattern, "/**") {
			prefix := strings.TrimSuffix(pattern, "/**")
			if candidate == prefix || strings.HasPrefix(candidate, prefix+"/") {
				return true
			}
			continue
		}
		if matched, _ := path.Match(pattern, candidate); matched {
			return true
		}
	}
	return false
}

func (c ContractRevision) Validate() error {
	if c.TaskID == "" || c.Revision < 1 || !commitPattern.MatchString(c.BaselineCommit) || strings.TrimSpace(c.Goal) == "" || len(c.Goal) > MaxGoalLength {
		return fmt.Errorf("%w: contract identity, baseline, goal, and revision are required", ErrInvalidRecord)
	}
	if len(c.AllowedPaths) == 0 || len(c.AllowedPaths) > MaxScopeEntries || len(c.ForbiddenActions) > MaxScopeEntries || len(c.AcceptanceCriteria) == 0 || len(c.AcceptanceCriteria) > MaxAcceptanceItems {
		return fmt.Errorf("%w: contract collections are invalid", ErrInvalidRecord)
	}
	if c.Risk != RiskLow && c.Risk != RiskMedium && c.Risk != RiskHigh {
		return fmt.Errorf("%w: risk is invalid", ErrInvalidRecord)
	}
	if c.CreatedAt.IsZero() || c.EventID == "" {
		return fmt.Errorf("%w: contract provenance is required", ErrInvalidRecord)
	}
	if err := validateUnique(c.AllowedPaths, true); err != nil {
		return err
	}
	if err := validateUnique(c.ForbiddenActions, false); err != nil {
		return err
	}
	if err := validateUnique(c.AcceptanceCriteria, false); err != nil {
		return err
	}
	return validateVerificationCommands(c.VerificationCommands)
}

func validateVerificationCommands(commands []VerificationCommand) error {
	if len(commands) > MaxVerificationCommands {
		return fmt.Errorf("%w: too many verification commands", ErrInvalidRecord)
	}
	seen := make(map[string]struct{}, len(commands))
	for _, command := range commands {
		if !verificationRulePattern.MatchString(command.RuleID) || len(command.Arguments) > MaxVerificationArguments {
			return fmt.Errorf("%w: verification rule or argument count is invalid", ErrInvalidRecord)
		}
		workingDirectory, err := normalizeWorkingDirectory(command.WorkingDirectory)
		if err != nil {
			return err
		}
		for _, argument := range command.Arguments {
			if argument == "" || len(argument) > MaxVerificationArgumentLength || strings.IndexByte(argument, 0) >= 0 {
				return fmt.Errorf("%w: verification argument is invalid", ErrInvalidRecord)
			}
		}
		key := command.RuleID + "\x00" + workingDirectory + "\x00" + strings.Join(command.Arguments, "\x00")
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate verification command", ErrInvalidRecord)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func normalizeWorkingDirectory(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "."
	}
	if len(value) > MaxContractTextSize || strings.ContainsAny(value, "*?[]") || strings.IndexByte(value, 0) >= 0 {
		return "", fmt.Errorf("%w: verification working directory is invalid", ErrInvalidRecord)
	}
	clean := filepath.Clean(filepath.FromSlash(value))
	if filepath.IsAbs(clean) || filepath.VolumeName(clean) != "" || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: verification working directory must remain repository-relative", ErrInvalidRecord)
	}
	return filepath.ToSlash(clean), nil
}

func validateUnique(values []string, paths bool) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > MaxContractTextSize {
			return fmt.Errorf("%w: contract entry is invalid", ErrInvalidRecord)
		}
		if paths {
			clean := filepath.Clean(filepath.FromSlash(value))
			if filepath.IsAbs(clean) || filepath.VolumeName(clean) != "" || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
				return fmt.Errorf("%w: allowed path must remain repository-relative", ErrInvalidRecord)
			}
			value = filepath.ToSlash(clean)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("%w: duplicate contract entry", ErrInvalidRecord)
		}
		seen[value] = struct{}{}
	}
	return nil
}
