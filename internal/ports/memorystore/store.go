package memorystore

import (
	"context"
	"errors"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
)

var (
	ErrInvalidCommand        = errors.New("invalid memory command")
	ErrNotFound              = errors.New("memory was not found")
	ErrRevisionConflict      = errors.New("memory revision conflict")
	ErrTransitionRejected    = errors.New("memory transition was rejected")
	ErrEvidenceNotFound      = errors.New("memory evidence was not found in the Vault")
	ErrIdempotencyConflict   = errors.New("memory idempotency conflict")
	ErrIdempotencyUnverified = errors.New("memory idempotency result cannot be verified")
)

type CreateCandidateInput struct {
	VaultID          string
	Kind             memory.Kind
	Scope            memory.Scope
	Statement        string
	Rationale        string
	Applicability    memory.Applicability
	EvidenceEventIDs []string
	SourceActor      string
	Confidence       int
	Sensitivity      event.Sensitivity
	OccurredAt       time.Time
	IdempotencyKey   string
}

type TransitionInput struct {
	VaultID          string
	MemoryID         string
	ExpectedRevision int
	NextState        memory.State
	Reason           string
	ExpiresAt        time.Time
	SupersededBy     string
	OccurredAt       time.Time
	IdempotencyKey   string
}

type ListActiveInput struct {
	VaultID     string
	WorkspaceID string
	Limit       int
	At          time.Time
}

type ListInput struct {
	VaultID string
	Limit   int
}

type Reader interface {
	GetMemory(context.Context, string, string) (memory.Record, error)
	ListMemoryCandidates(context.Context, string, int) ([]memory.Record, error)
	ListActiveMemories(context.Context, ListActiveInput) ([]memory.Record, error)
	ListMemories(context.Context, ListInput) ([]memory.Record, error)
}

type Store interface {
	Reader
	CreateMemoryCandidate(context.Context, CreateCandidateInput) (memory.Record, error)
	TransitionMemory(context.Context, TransitionInput) (memory.Record, error)
}
