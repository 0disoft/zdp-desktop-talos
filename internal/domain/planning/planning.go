package planning

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

const (
	SchemaVersion        = 1
	MaxPlanSteps         = 8
	MaxSummaryLength     = 2048
	MaxStepPurpose       = 1024
	MaxSafeErrorLength   = 128
	MaxProviderKeyLength = 64
)

var (
	ErrInvalidPlan    = errors.New("invalid model plan")
	ErrInvalidReceipt = errors.New("invalid model egress receipt")
	keyPattern        = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)
	hashPattern       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	errorCodePattern  = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
)

type ToolKind string

const ToolVerificationCommand ToolKind = "verification_command"

type ToolIntent struct {
	ID           string   `json:"id"`
	Kind         ToolKind `json:"kind"`
	CommandIndex int      `json:"command_index"`
}

type Step struct {
	ID      string     `json:"id"`
	Purpose string     `json:"purpose"`
	Tool    ToolIntent `json:"tool"`
}

type Plan struct {
	SchemaVersion int    `json:"schema_version"`
	Summary       string `json:"summary"`
	Steps         []Step `json:"steps"`
}

func (p Plan) Validate() error {
	if p.SchemaVersion != SchemaVersion || strings.TrimSpace(p.Summary) == "" || len(p.Summary) > MaxSummaryLength || len(p.Steps) == 0 || len(p.Steps) > MaxPlanSteps {
		return ErrInvalidPlan
	}
	stepIDs := make(map[string]struct{}, len(p.Steps))
	toolIDs := make(map[string]struct{}, len(p.Steps))
	commandIndexes := make(map[int]struct{}, len(p.Steps))
	for _, step := range p.Steps {
		if !keyPattern.MatchString(step.ID) || strings.TrimSpace(step.Purpose) == "" || len(step.Purpose) > MaxStepPurpose || !keyPattern.MatchString(step.Tool.ID) || step.Tool.Kind != ToolVerificationCommand || step.Tool.CommandIndex < 0 {
			return ErrInvalidPlan
		}
		if _, exists := stepIDs[step.ID]; exists {
			return ErrInvalidPlan
		}
		if _, exists := toolIDs[step.Tool.ID]; exists {
			return ErrInvalidPlan
		}
		if _, exists := commandIndexes[step.Tool.CommandIndex]; exists {
			return ErrInvalidPlan
		}
		stepIDs[step.ID] = struct{}{}
		toolIDs[step.Tool.ID] = struct{}{}
		commandIndexes[step.Tool.CommandIndex] = struct{}{}
	}
	return nil
}

type EgressStatus string

const (
	EgressPrepared  EgressStatus = "prepared"
	EgressCompleted EgressStatus = "completed"
	EgressFailed    EgressStatus = "failed"
)

type Usage struct {
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
}

func (u Usage) Validate() error {
	if u.InputTokens < 0 || u.CachedInputTokens < 0 || u.OutputTokens < 0 || u.CachedInputTokens > u.InputTokens {
		return ErrInvalidReceipt
	}
	return nil
}

type EgressReceipt struct {
	ID                   string
	VaultID              string
	TaskID               string
	ContractRevision     int
	ProviderKey          string
	ModelKey             string
	RequestID            string
	PromptVersion        string
	ContextHash          string
	RequestHash          string
	ResponseHash         string
	ProviderCallID       string
	ContextItems         int
	InputBytes           int
	OutputBytes          int
	RedactionCount       int
	ReservedInputTokens  int
	ReservedOutputTokens int
	Usage                Usage
	Status               EgressStatus
	SafeErrorCode        string
	CreatedAt            time.Time
	UpdatedAt            time.Time
	CreatedEventID       string
	LastEventID          string
}

func (r EgressReceipt) Validate() error {
	if r.ID == "" || r.VaultID == "" || r.TaskID == "" || r.ContractRevision < 1 || !keyPattern.MatchString(r.ProviderKey) || !keyPattern.MatchString(r.ModelKey) || r.RequestID == "" || len(r.RequestID) > 96 || !keyPattern.MatchString(r.PromptVersion) || !hashPattern.MatchString(r.ContextHash) || !hashPattern.MatchString(r.RequestHash) || r.ContextItems < 1 || r.ContextItems > 64 || r.InputBytes < 1 || r.InputBytes > 1<<20 || r.OutputBytes < 0 || r.OutputBytes > 1<<20 || r.RedactionCount < 0 || r.RedactionCount > 10000 || r.ReservedInputTokens < 0 || r.ReservedOutputTokens < 0 || r.CreatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) || r.CreatedEventID == "" || r.LastEventID == "" || r.Usage.Validate() != nil {
		return ErrInvalidReceipt
	}
	switch r.Status {
	case EgressPrepared:
		legacyUnreserved := r.ReservedInputTokens == 0 && r.ReservedOutputTokens == 0
		boundedReservation := r.ReservedInputTokens >= 1 && r.ReservedOutputTokens >= 256
		if r.ResponseHash != "" || r.ProviderCallID != "" || r.OutputBytes != 0 || r.SafeErrorCode != "" || r.Usage != (Usage{}) || (!legacyUnreserved && !boundedReservation) {
			return ErrInvalidReceipt
		}
	case EgressCompleted:
		if !hashPattern.MatchString(r.ResponseHash) || !ValidOpaqueID(r.ProviderCallID) || r.OutputBytes == 0 || r.SafeErrorCode != "" || r.ReservedInputTokens != 0 || r.ReservedOutputTokens != 0 {
			return ErrInvalidReceipt
		}
	case EgressFailed:
		if r.ResponseHash != "" || r.ProviderCallID != "" || r.OutputBytes != 0 || !errorCodePattern.MatchString(r.SafeErrorCode) || r.ReservedInputTokens != 0 || r.ReservedOutputTokens != 0 {
			return ErrInvalidReceipt
		}
	default:
		return ErrInvalidReceipt
	}
	return nil
}

func ValidKey(value string) bool {
	return len(value) <= MaxProviderKeyLength && keyPattern.MatchString(value)
}

func ValidOpaqueID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !(character == '-' || character == '_' || character == '.' || character == ':' || character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9') {
			return false
		}
	}
	return true
}
