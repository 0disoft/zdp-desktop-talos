package patchaction

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

var (
	ErrInvalidRecord = errors.New("invalid patch action record")
	hashPattern      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Kind string

const (
	KindApply   Kind = "apply"
	KindDiscard Kind = "discard"
)

type State string

const (
	StatePending   State = "pending"
	StateSucceeded State = "succeeded"
	StateFailed    State = "failed"
	StateUnknown   State = "unknown"
)

type Record struct {
	ID                string
	VaultID           string
	TaskID            string
	Kind              Kind
	State             State
	ContractRevision  int
	PatchHash         string
	WorktreeStateHash string
	EvidenceID        string
	SafeErrorCode     string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	CreatedEventID    string
	LastEventID       string
}

func (r Record) Validate() error {
	if r.ID == "" || r.VaultID == "" || r.TaskID == "" || (r.Kind != KindApply && r.Kind != KindDiscard) || (r.State != StatePending && r.State != StateSucceeded && r.State != StateFailed && r.State != StateUnknown) || r.ContractRevision < 1 || !hashPattern.MatchString(r.PatchHash) || !hashPattern.MatchString(r.WorktreeStateHash) || r.CreatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) || r.CreatedEventID == "" || r.LastEventID == "" {
		return fmt.Errorf("%w: identity, state, binding, or provenance is invalid", ErrInvalidRecord)
	}
	if r.Kind == KindApply && r.EvidenceID == "" {
		return fmt.Errorf("%w: apply requires verification evidence", ErrInvalidRecord)
	}
	if r.State == StatePending && r.SafeErrorCode != "" || (r.State == StateFailed || r.State == StateUnknown) && r.SafeErrorCode == "" || r.State == StateSucceeded && r.SafeErrorCode != "" {
		return fmt.Errorf("%w: terminal error metadata is invalid", ErrInvalidRecord)
	}
	return nil
}
