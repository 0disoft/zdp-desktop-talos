package worktree

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecordValidation(t *testing.T) {
	valid := Record{TaskID: "01900000-0000-7000-8000-000000000001", RepositoryRoot: filepath.Join(t.TempDir(), "primary"), Root: filepath.Join(t.TempDir(), "task"), BaselineCommit: strings.Repeat("a", 40), CreatedAt: time.Now()}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.Root = invalid.RepositoryRoot
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("same root error=%v", err)
	}
}
