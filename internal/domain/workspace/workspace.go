package workspace

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const MaxChanges = 4096

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40,64}$`)

var ErrInvalidSnapshot = errors.New("invalid repository snapshot")

type ChangeKind string

const (
	ChangeTracked   ChangeKind = "tracked"
	ChangeRenamed   ChangeKind = "renamed"
	ChangeUntracked ChangeKind = "untracked"
)

type Change struct {
	Path           string
	OriginalPath   string
	Kind           ChangeKind
	IndexStatus    byte
	WorktreeStatus byte
}

type RepositorySnapshot struct {
	Root           string
	BaselineCommit string
	HeadRef        string
	Detached       bool
	Dirty          bool
	Changes        []Change
	CapturedAt     time.Time
}

func (s RepositorySnapshot) Validate() error {
	if !filepath.IsAbs(s.Root) || !commitPattern.MatchString(s.BaselineCommit) || s.CapturedAt.IsZero() {
		return fmt.Errorf("%w: root, baseline commit, and capture time are required", ErrInvalidSnapshot)
	}
	if s.Detached == (s.HeadRef != "") {
		return fmt.Errorf("%w: head ref and detached state disagree", ErrInvalidSnapshot)
	}
	if len(s.Changes) > MaxChanges || s.Dirty != (len(s.Changes) > 0) {
		return fmt.Errorf("%w: dirty state or change count is invalid", ErrInvalidSnapshot)
	}
	for _, change := range s.Changes {
		if err := change.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (c Change) Validate() error {
	if !validRelativePath(c.Path) {
		return fmt.Errorf("%w: change path is invalid", ErrInvalidSnapshot)
	}
	switch c.Kind {
	case ChangeTracked:
		if c.OriginalPath != "" || (c.IndexStatus == '.' && c.WorktreeStatus == '.') {
			return fmt.Errorf("%w: tracked change metadata is invalid", ErrInvalidSnapshot)
		}
	case ChangeRenamed:
		if !validRelativePath(c.OriginalPath) {
			return fmt.Errorf("%w: renamed change origin is invalid", ErrInvalidSnapshot)
		}
	case ChangeUntracked:
		if c.OriginalPath != "" || c.IndexStatus != '?' || c.WorktreeStatus != '?' {
			return fmt.Errorf("%w: untracked change metadata is invalid", ErrInvalidSnapshot)
		}
	default:
		return fmt.Errorf("%w: unknown change kind", ErrInvalidSnapshot)
	}
	return nil
}

func validRelativePath(value string) bool {
	if value == "" || strings.IndexByte(value, 0) >= 0 || filepath.IsAbs(value) || filepath.VolumeName(value) != "" {
		return false
	}
	clean := filepath.Clean(filepath.FromSlash(value))
	return clean != "." && clean != ".." && !filepath.IsAbs(clean) && len(clean) <= 4096 && clean[:1] != string(filepath.Separator) && (len(clean) < 3 || clean[:3] != ".."+string(filepath.Separator))
}
