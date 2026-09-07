package patchstore

import (
	"context"
	"errors"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/patchaction"
)

var (
	ErrInvalidCommand      = errors.New("invalid patch action command")
	ErrNotFound            = errors.New("patch action was not found")
	ErrConflict            = errors.New("patch action state conflict")
	ErrBlockingDecision    = errors.New("blocking decision prevents patch apply")
	ErrIdempotencyConflict = errors.New("patch action idempotency conflict")
)

type PrepareInput struct {
	VaultID           string
	TaskID            string
	Kind              patchaction.Kind
	ContractRevision  int
	PatchHash         string
	WorktreeStateHash string
	EvidenceID        string
	OccurredAt        time.Time
	IdempotencyKey    string
}

type Prepared struct {
	Action   patchaction.Record
	Replayed bool
}

// ReplayInput contains only the caller-owned identity of a patch command.
// Generated timestamps, evidence selection and current worktree state do not
// change the identity of a request whose outcome has already been journaled.
type ReplayInput struct {
	VaultID          string
	TaskID           string
	Kind             patchaction.Kind
	ContractRevision int
	PatchHash        string
	IdempotencyKey   string
}

type FinishInput struct {
	VaultID        string
	ActionID       string
	ExpectedState  patchaction.State
	NextState      patchaction.State
	SafeErrorCode  string
	OccurredAt     time.Time
	IdempotencyKey string
}

type Store interface {
	FindPatchAction(context.Context, ReplayInput) (patchaction.Record, error)
	PreparePatchAction(context.Context, PrepareInput) (Prepared, error)
	FinishPatchAction(context.Context, FinishInput) (patchaction.Record, error)
}
