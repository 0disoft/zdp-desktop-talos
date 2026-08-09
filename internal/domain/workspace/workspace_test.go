package workspace

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSnapshotRejectsPathEscapeAndStateMismatch(t *testing.T) {
	root := t.TempDir()
	base := RepositorySnapshot{
		Root: root, BaselineCommit: strings.Repeat("a", 40), HeadRef: "main",
		Changes: []Change{{Path: "src/main.go", Kind: ChangeTracked, IndexStatus: 'M', WorktreeStatus: '.'}},
		Dirty:   true, CapturedAt: time.Now().UTC(),
	}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	invalid := base
	invalid.Changes = []Change{{Path: filepath.Join("..", "secret"), Kind: ChangeTracked, IndexStatus: 'M', WorktreeStatus: '.'}}
	if err := invalid.Validate(); err == nil {
		t.Fatal("parent traversal was accepted")
	}
	invalid = base
	invalid.Changes = []Change{{Path: "C:/outside", Kind: ChangeTracked, IndexStatus: 'M', WorktreeStatus: '.'}}
	if err := invalid.Validate(); err == nil {
		t.Fatal("Windows drive path was accepted")
	}
	invalid = base
	invalid.Changes = []Change{{Path: `\\server\share`, Kind: ChangeTracked, IndexStatus: 'M', WorktreeStatus: '.'}}
	if err := invalid.Validate(); err == nil {
		t.Fatal("UNC path was accepted")
	}
	invalid = base
	invalid.Changes = []Change{{Path: "/outside", Kind: ChangeTracked, IndexStatus: 'M', WorktreeStatus: '.'}}
	if err := invalid.Validate(); err == nil {
		t.Fatal("POSIX absolute path was accepted")
	}
	invalid = base
	invalid.Dirty = false
	if err := invalid.Validate(); err == nil {
		t.Fatal("dirty state mismatch was accepted")
	}
	invalid = base
	invalid.Detached = true
	if err := invalid.Validate(); err == nil {
		t.Fatal("detached snapshot retained a branch name")
	}
}
