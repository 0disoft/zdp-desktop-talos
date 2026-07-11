package worktree

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"time"
)

var (
	ErrInvalidRecord = errors.New("invalid task worktree record")
	commitPattern    = regexp.MustCompile(`^[0-9a-f]{40,64}$`)
	taskIDPattern    = regexp.MustCompile(`^[0-9a-f-]{36}$`)
)

type Record struct {
	TaskID         string
	RepositoryRoot string
	Root           string
	BaselineCommit string
	CreatedAt      time.Time
}

func (r Record) Validate() error {
	if !taskIDPattern.MatchString(r.TaskID) || !filepath.IsAbs(r.RepositoryRoot) || !filepath.IsAbs(r.Root) || !commitPattern.MatchString(r.BaselineCommit) || r.CreatedAt.IsZero() {
		return fmt.Errorf("%w: identity, path, baseline, or time is invalid", ErrInvalidRecord)
	}
	if filepath.Clean(r.RepositoryRoot) == filepath.Clean(r.Root) {
		return fmt.Errorf("%w: task and primary roots must differ", ErrInvalidRecord)
	}
	return nil
}
