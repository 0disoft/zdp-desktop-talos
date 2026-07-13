package taskstore

import (
	"context"
	"errors"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
)

var (
	ErrInvalidCommand        = errors.New("invalid task command")
	ErrNotFound              = errors.New("task was not found")
	ErrAlreadyExists         = errors.New("task already exists")
	ErrIdempotencyConflict   = errors.New("task idempotency key conflict")
	ErrIdempotencyUnverified = errors.New("task idempotency result cannot be verified")
	ErrRevisionConflict      = errors.New("task revision conflict")
)

type CreateInput struct {
	VaultID              string
	WorkspaceRoot        string
	BaselineCommit       string
	Goal                 string
	AllowedPaths         []string
	ForbiddenActions     []string
	AcceptanceCriteria   []string
	VerificationCommands []task.VerificationCommand
	Risk                 task.Risk
	OccurredAt           time.Time
	IdempotencyKey       string
}

type Created struct {
	Task     task.Record
	Contract task.ContractRevision
}

type ReviseInput struct {
	VaultID              string
	TaskID               string
	ExpectedRevision     int
	Goal                 string
	AllowedPaths         []string
	ForbiddenActions     []string
	AcceptanceCriteria   []string
	VerificationCommands []task.VerificationCommand
	Risk                 task.Risk
	OccurredAt           time.Time
	IdempotencyKey       string
}

type Store interface {
	CreateTaskContract(context.Context, CreateInput) (Created, error)
	ReviseTaskContract(context.Context, ReviseInput) (Created, error)
	GetTask(context.Context, string) (task.Record, error)
	GetTaskContract(context.Context, string, int) (task.ContractRevision, error)
}
