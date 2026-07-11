package execution

import (
	"errors"
	"strings"
	"time"
)

var ErrInvalidRecord = errors.New("invalid execution record")

type RunState string

const (
	RunActive    RunState = "active"
	RunCompleted RunState = "completed"
	RunFailed    RunState = "failed"
	RunCanceled  RunState = "canceled"
	RunUnknown   RunState = "unknown"
)

type AttemptState string

const (
	AttemptDispatchPending AttemptState = "dispatch_pending"
	AttemptSucceeded       AttemptState = "succeeded"
	AttemptFailed          AttemptState = "failed"
	AttemptCanceled        AttemptState = "canceled"
	AttemptUnknown         AttemptState = "unknown"
)

type Run struct {
	ID            string
	VaultID       string
	TaskID        string
	WorkspaceHash string
	State         RunState
	CreatedAt     time.Time
	UpdatedAt     time.Time
	LastEventID   string
}

type Attempt struct {
	ID             string
	RunID          string
	CallID         string
	CapabilityHash string
	GrantID        string
	State          AttemptState
	ExitCode       *int
	SafeErrorCode  string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastEventID    string
}

func (r Run) Validate() error {
	if r.ID == "" || r.VaultID == "" || r.TaskID == "" || !validHash(r.WorkspaceHash) || r.CreatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) || r.LastEventID == "" {
		return ErrInvalidRecord
	}
	switch r.State {
	case RunActive, RunCompleted, RunFailed, RunCanceled, RunUnknown:
		return nil
	default:
		return ErrInvalidRecord
	}
}

func (a Attempt) Validate() error {
	if a.ID == "" || a.RunID == "" || a.CallID == "" || !validHash(a.CapabilityHash) || a.CreatedAt.IsZero() || a.UpdatedAt.Before(a.CreatedAt) || a.LastEventID == "" || len(a.SafeErrorCode) > 128 {
		return ErrInvalidRecord
	}
	switch a.State {
	case AttemptDispatchPending:
		if a.ExitCode != nil || a.SafeErrorCode != "" {
			return ErrInvalidRecord
		}
	case AttemptSucceeded:
		if a.ExitCode == nil || *a.ExitCode != 0 || a.SafeErrorCode != "" {
			return ErrInvalidRecord
		}
	case AttemptFailed:
		if a.ExitCode == nil && a.SafeErrorCode == "" {
			return ErrInvalidRecord
		}
	case AttemptCanceled, AttemptUnknown:
		if a.ExitCode != nil || a.SafeErrorCode == "" {
			return ErrInvalidRecord
		}
	default:
		return ErrInvalidRecord
	}
	return nil
}

func validHash(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
