package repository

import (
	"context"
	"errors"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspace"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/worktree"
)

var (
	ErrWorktreeExists         = errors.New("task worktree already exists")
	ErrWorktreeNotFound       = errors.New("task worktree not found")
	ErrWorktreeOwnership      = errors.New("task worktree ownership cannot be verified")
	ErrWorktreeUnsafeConfig   = errors.New("repository configuration can execute checkout filters")
	ErrWorktreeCreateFailed   = errors.New("task worktree creation failed")
	ErrWorktreeRemoveFailed   = errors.New("task worktree removal failed")
	ErrWorktreeSnapshotFailed = errors.New("task worktree state snapshot failed")
	ErrPatchConflict          = errors.New("patch no longer matches the expected repository state")
	ErrPatchApplyFailed       = errors.New("patch cannot be applied to the primary worktree")
	ErrPatchOutcomeUnknown    = errors.New("patch mutation outcome is unknown")
)

type CreateWorktreeInput struct {
	TaskID         string
	RepositoryRoot string
	BaselineCommit string
	CreatedAt      time.Time
}

type WorktreeManager interface {
	Create(context.Context, CreateWorktreeInput) (worktree.Record, error)
	Open(context.Context, CreateWorktreeInput) (worktree.Record, error)
	Snapshot(context.Context, worktree.Record) (WorktreeState, error)
	Remove(context.Context, worktree.Record) error
}

type WorktreeState struct {
	Hash string
}

type WorktreeReview struct {
	StateHash string
	PatchHash string
	Changes   []workspace.Change
	Diffs     []FileDiff
}

type FileDiff struct {
	Path          string
	Binary        bool
	Truncated     bool
	OmittedReason string
	Text          string
	AddedLines    int
	DeletedLines  int
}
