package gitcli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0disoft/zdp-desktop-talos/internal/ports/repository"
)

const testTaskID = "01900000-0000-7000-8000-000000000001"

func TestWorktreeLifecycleLeavesPrimaryUntouched(t *testing.T) {
	t.Parallel()
	git, primary, baseline := createWorktreeTestRepository(t)
	if err := os.WriteFile(filepath.Join(primary, "tracked.txt"), []byte("primary dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(primary, "untracked.txt"), []byte("primary only\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeHead := runGitOutput(t, git, primary, "rev-parse", "HEAD")
	beforeStatus := runGitOutput(t, git, primary, "status", "--porcelain=v1", "--untracked-files=all")
	manager, err := NewWorktreeManager(filepath.Join(t.TempDir(), "talos-owned"))
	if err != nil {
		t.Fatal(err)
	}
	record, err := manager.Create(context.Background(), repository.CreateWorktreeInput{TaskID: testTaskID, RepositoryRoot: primary, BaselineCommit: baseline})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(runGitOutput(t, git, record.Root, "rev-parse", "HEAD")); got != baseline {
		t.Fatalf("task HEAD=%s", got)
	}
	if got := runGitOutput(t, git, record.Root, "status", "--porcelain=v1", "--untracked-files=all"); got != "" {
		t.Fatalf("task worktree dirty: %q", got)
	}
	if err := os.WriteFile(filepath.Join(record.Root, "tracked.txt"), []byte("task changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(record.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("task root still exists: %v", err)
	}
	if after := runGitOutput(t, git, primary, "rev-parse", "HEAD"); after != beforeHead {
		t.Fatalf("primary HEAD changed: %q != %q", after, beforeHead)
	}
	if after := runGitOutput(t, git, primary, "status", "--porcelain=v1", "--untracked-files=all"); after != beforeStatus {
		t.Fatalf("primary status changed: %q != %q", after, beforeStatus)
	}
}

func TestWorktreeSnapshotChangesWithTrackedAndUntrackedContent(t *testing.T) {
	t.Parallel()
	_, primary, baseline := createWorktreeTestRepository(t)
	manager, err := NewWorktreeManager(filepath.Join(t.TempDir(), "talos-owned"))
	if err != nil {
		t.Fatal(err)
	}
	record, err := manager.Create(context.Background(), repository.CreateWorktreeInput{TaskID: testTaskID, RepositoryRoot: primary, BaselineCommit: baseline})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := manager.Snapshot(context.Background(), record)
	if err != nil || len(initial.Hash) != 64 {
		t.Fatalf("initial=%+v error=%v", initial, err)
	}
	if err := os.WriteFile(filepath.Join(record.Root, "tracked.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tracked, err := manager.Snapshot(context.Background(), record)
	if err != nil || tracked.Hash == initial.Hash {
		t.Fatalf("tracked=%+v initial=%+v error=%v", tracked, initial, err)
	}
	if err := os.WriteFile(filepath.Join(record.Root, "untracked.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	untracked, err := manager.Snapshot(context.Background(), record)
	if err != nil || untracked.Hash == tracked.Hash {
		t.Fatalf("untracked=%+v tracked=%+v error=%v", untracked, tracked, err)
	}
}

func TestWorktreeOpenVerifiesOwnedMarkerAndBaseline(t *testing.T) {
	t.Parallel()
	_, primary, baseline := createWorktreeTestRepository(t)
	manager, err := NewWorktreeManager(filepath.Join(t.TempDir(), "talos-owned"))
	if err != nil {
		t.Fatal(err)
	}
	input := repository.CreateWorktreeInput{TaskID: testTaskID, RepositoryRoot: primary, BaselineCommit: baseline}
	created, err := manager.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(created.Root, "tracked.txt"), []byte("review patch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened, err := manager.Open(context.Background(), input)
	if err != nil || reopened.Root != created.Root || reopened.CreatedAt != created.CreatedAt {
		t.Fatalf("reopened=%+v created=%+v error=%v", reopened, created, err)
	}
	tampered := input
	tampered.BaselineCommit = strings.Repeat("b", 40)
	if _, err := manager.Open(context.Background(), tampered); !errors.Is(err, repository.ErrWorktreeOwnership) {
		t.Fatalf("tampered baseline error=%v", err)
	}
}

func TestWorktreeRejectsExecutableCheckoutFilters(t *testing.T) {
	t.Parallel()
	git, primary, baseline := createWorktreeTestRepository(t)
	runGitTest(t, git, primary, "config", "filter.danger.smudge", "dangerous-program")
	manager, err := NewWorktreeManager(filepath.Join(t.TempDir(), "talos-owned"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Create(context.Background(), repository.CreateWorktreeInput{TaskID: testTaskID, RepositoryRoot: primary, BaselineCommit: baseline})
	if !errors.Is(err, repository.ErrWorktreeUnsafeConfig) {
		t.Fatalf("filter error=%v", err)
	}
}

func TestWorktreeRemovalRequiresUntamperedOwnerMarker(t *testing.T) {
	t.Parallel()
	_, primary, baseline := createWorktreeTestRepository(t)
	ownedRoot := filepath.Join(t.TempDir(), "talos-owned")
	manager, err := NewWorktreeManager(ownedRoot)
	if err != nil {
		t.Fatal(err)
	}
	record, err := manager.Create(context.Background(), repository.CreateWorktreeInput{TaskID: testTaskID, RepositoryRoot: primary, BaselineCommit: baseline})
	if err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(ownedRoot, "owners", testTaskID+".json")
	if err := os.WriteFile(markerPath, []byte(`{"schema":"tampered"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(context.Background(), record); !errors.Is(err, repository.ErrWorktreeOwnership) {
		t.Fatalf("ownership error=%v", err)
	}
	if _, err := os.Stat(record.Root); err != nil {
		t.Fatalf("owned worktree was removed after marker tamper: %v", err)
	}
}

func createWorktreeTestRepository(t *testing.T) (string, string, string) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("system Git is unavailable")
	}
	primary := t.TempDir()
	runGitTest(t, git, primary, "init")
	runGitTest(t, git, primary, "config", "user.email", "talos-test@example.invalid")
	runGitTest(t, git, primary, "config", "user.name", "Talos Test")
	if err := os.WriteFile(filepath.Join(primary, "tracked.txt"), []byte("baseline\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, git, primary, "add", "tracked.txt")
	runGitTest(t, git, primary, "commit", "-m", "baseline")
	return git, primary, strings.TrimSpace(runGitOutput(t, git, primary, "rev-parse", "HEAD"))
}

func runGitOutput(t *testing.T, executable, directory string, args ...string) string {
	t.Helper()
	command := exec.Command(executable, append([]string{"-C", directory}, args...)...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(output)
}
