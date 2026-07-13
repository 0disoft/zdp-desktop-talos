package repository

import (
	"context"
	"errors"
	"time"

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
