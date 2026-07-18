package gitcli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspace"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/repository"
)

func TestParseStatusHandlesTrackedRenameConflictAndUntracked(t *testing.T) {
	output := []byte("1 M. N... 100644 100644 100644 aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa src/main.go\x00" +
		"2 R. N... 100644 100644 100644 bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb R100 new name.txt\x00old name.txt\x00" +
		"u UU N... 100644 100644 100644 100644 cccccccccccccccccccccccccccccccccccccccc dddddddddddddddddddddddddddddddddddddddd eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee conflict.txt\x00" +
		"? notes draft.md\x00")
	changes, err := parseStatus(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 4 {
		t.Fatalf("changes=%+v", changes)
	}
	var renamed workspace.Change
	for _, change := range changes {
		if change.Kind == workspace.ChangeRenamed {
			renamed = change
		}
	}
	if renamed.Path != "new name.txt" || renamed.OriginalPath != "old name.txt" {
		t.Fatalf("renamed=%+v", renamed)
	}
}

func TestParseStatusRejectsTraversalAndExcessChanges(t *testing.T) {
	if _, err := parseStatus([]byte("? ../secret\x00")); !errors.Is(err, repository.ErrInspectionFailed) {
		t.Fatalf("traversal error=%v", err)
	}
	var output bytes.Buffer
	for index := 0; index <= workspace.MaxChanges; index++ {
		output.WriteString("? file-")
		output.WriteString(strings.Repeat("0", 8))
		output.WriteByte('-')
		output.WriteString(string(rune('a' + index%26)))
		output.WriteByte(0)
	}
	if _, err := parseStatus(output.Bytes()); !errors.Is(err, repository.ErrOutputLimit) {
		t.Fatalf("change limit error=%v", err)
	}
}

func TestInspectorReadsCleanDirtyAndDetachedRepository(t *testing.T) {
	t.Parallel()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("system Git is unavailable")
	}
	root := t.TempDir()
	runGitTest(t, git, root, "init")
	runGitTest(t, git, root, "config", "user.email", "talos-test@example.invalid")
	runGitTest(t, git, root, "config", "user.name", "Talos Test")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("baseline\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, git, root, "add", "tracked.txt")
	runGitTest(t, git, root, "commit", "-m", "baseline")
	subdirectory := filepath.Join(root, "nested")
	if err := os.Mkdir(subdirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	inspector, err := New()
	if err != nil {
		t.Fatal(err)
	}
	clean, err := inspector.Inspect(context.Background(), subdirectory)
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, _ := filepath.EvalSymlinks(root)
	if clean.Root != canonicalRoot || clean.Dirty || clean.Detached || clean.HeadRef == "" {
		t.Fatalf("clean=%+v", clean)
	}
	contains, err := inspector.ContainsCommit(context.Background(), clean.Root, clean.BaselineCommit)
	if err != nil || !contains {
		t.Fatalf("contains baseline=%v error=%v", contains, err)
	}
	contains, err = inspector.ContainsCommit(context.Background(), clean.Root, strings.Repeat("f", 40))
	if err != nil || contains {
		t.Fatalf("contains missing=%v error=%v", contains, err)
	}
	if _, err := inspector.ContainsCommit(context.Background(), clean.Root, "HEAD"); !errors.Is(err, repository.ErrInvalidCommit) {
		t.Fatalf("invalid commit error=%v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirty, err := inspector.Inspect(context.Background(), root)
	if err != nil || !dirty.Dirty || len(dirty.Changes) != 2 {
		t.Fatalf("dirty=%+v err=%v", dirty, err)
	}
	runGitTest(t, git, root, "checkout", "--detach")
	detached, err := inspector.Inspect(context.Background(), root)
	if err != nil || !detached.Detached || detached.HeadRef != "" {
		t.Fatalf("detached=%+v err=%v", detached, err)
	}
}

func TestInspectorRejectsUnbornAndBareRepositories(t *testing.T) {
	t.Parallel()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("system Git is unavailable")
	}
	inspector, err := New()
	if err != nil {
		t.Fatal(err)
	}
	unborn := t.TempDir()
	runGitTest(t, git, unborn, "init")
	if _, err := inspector.Inspect(context.Background(), unborn); !errors.Is(err, repository.ErrNoBaselineCommit) {
		t.Fatalf("unborn error=%v", err)
	}
	bare := filepath.Join(t.TempDir(), "bare.git")
	runGitTest(t, git, filepath.Dir(bare), "init", "--bare", bare)
	if _, err := inspector.Inspect(context.Background(), bare); !errors.Is(err, repository.ErrNotRepository) {
		t.Fatalf("bare error=%v", err)
	}
}

func runGitTest(t *testing.T, executable, directory string, args ...string) {
	t.Helper()
	command := exec.Command(executable, append([]string{"-C", directory}, args...)...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
