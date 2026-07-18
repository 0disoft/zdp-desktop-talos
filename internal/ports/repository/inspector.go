package repository

import (
	"context"
	"errors"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspace"
)

var (
	ErrInvalidPath      = errors.New("repository path is invalid")
	ErrInvalidCommit    = errors.New("repository commit is invalid")
	ErrGitUnavailable   = errors.New("system Git is unavailable")
	ErrNotRepository    = errors.New("path is not inside a supported Git worktree")
	ErrNoBaselineCommit = errors.New("repository has no baseline commit")
	ErrInspectionFailed = errors.New("repository inspection failed")
	ErrOutputLimit      = errors.New("repository inspection output limit exceeded")
)

type Inspector interface {
	Inspect(context.Context, string) (workspace.RepositorySnapshot, error)
}

type BaselineVerifier interface {
	ContainsCommit(context.Context, string, string) (bool, error)
}
