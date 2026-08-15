package executionstore

import (
	"context"
	"errors"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/execution"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/permission"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/taskbudget"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/verification"
)

var (
	ErrInvalidCommand      = errors.New("invalid execution journal command")
	ErrNotFound            = errors.New("execution journal record was not found")
	ErrConflict            = errors.New("execution journal state conflict")
	ErrIdempotencyConflict = errors.New("execution journal idempotency conflict")
	ErrGrantUnavailable    = errors.New("permission grant is unavailable")
	ErrBudgetExceeded      = errors.New("task execution budget exceeded")
)

type SaveGrantInput struct {
	VaultID        string
	Grant          permission.Grant
	OccurredAt     time.Time
	IdempotencyKey string
}

type CreatePermissionRequestInput struct {
	VaultID        string
	Intent         permission.ProcessIntent
	OccurredAt     time.Time
	IdempotencyKey string
}

type ResolvePermissionRequestInput struct {
	VaultID        string
	RequestID      string
	Outcome        permission.Outcome
	ExpiresAt      time.Time
	OccurredAt     time.Time
	IdempotencyKey string
}

type PermissionResolution struct {
	Request permission.Request
	Grant   permission.Grant
}

type PrepareAttemptInput struct {
	VaultID        string
	TaskID         string
	WorkspaceHash  string
	CapabilityHash string
	GrantID        string
	Budget         taskbudget.Policy
	OccurredAt     time.Time
	IdempotencyKey string
}

type Prepared struct {
	Run      execution.Run
	Attempt  execution.Attempt
	Evidence *verification.Evidence
	Replayed bool
}

type FinishAttemptInput struct {
	VaultID        string
	AttemptID      string
	ExpectedState  execution.AttemptState
	NextState      execution.AttemptState
	ExitCode       *int
	SafeErrorCode  string
	OccurredAt     time.Time
	IdempotencyKey string
	Evidence       *VerificationEvidenceInput
}

type VerificationEvidenceInput struct {
	TaskID            string
	ContractRevision  int
	CommandIndex      int
	BaselineCommit    string
	WorktreeStateHash string
	CapabilityHash    string
	StartedAt         time.Time
	FinishedAt        time.Time
}

type FinishedAttempt struct {
	Attempt  execution.Attempt
	Evidence *verification.Evidence
}

type FinishExecutionInput struct {
	Attempt FinishAttemptInput
	Run     FinishRunInput
}

type FinishedExecution struct {
	Attempt  execution.Attempt
	Run      execution.Run
	Evidence *verification.Evidence
}

type FinishRunInput struct {
	VaultID        string
	RunID          string
	ExpectedState  execution.RunState
	NextState      execution.RunState
	OccurredAt     time.Time
	IdempotencyKey string
}

type Store interface {
	CreatePermissionRequest(context.Context, CreatePermissionRequestInput) (permission.Request, error)
	ListOpenPermissionRequests(context.Context, string, string, int) ([]permission.Request, error)
	ResolvePermissionRequest(context.Context, ResolvePermissionRequestInput) (PermissionResolution, error)
	SavePermissionGrant(context.Context, SaveGrantInput) (permission.Grant, error)
	ListActivePermissionGrants(context.Context, string, string) ([]permission.Grant, error)
	PrepareAttempt(context.Context, PrepareAttemptInput) (Prepared, error)
	FinishExecution(context.Context, FinishExecutionInput) (FinishedExecution, error)
	FinishAttempt(context.Context, FinishAttemptInput) (FinishedAttempt, error)
	FinishRun(context.Context, FinishRunInput) (execution.Run, error)
	ReconcilePendingAttempts(context.Context, string, time.Time) (int, error)
}
