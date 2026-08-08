package gitcli

import (
	"context"
	"errors"
	"fmt"
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

func TestApplyCopiesTrackedAndUntrackedChangesWithoutMovingPrimaryHead(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(record.Root, "tracked.txt"), []byte("applied\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(record.Root, "new.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	review, err := manager.Review(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(context.Background(), record, review.PatchHash); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(runGitOutput(t, "git", primary, "rev-parse", "HEAD")); got != baseline {
		t.Fatalf("primary HEAD=%s", got)
	}
	tracked, err := os.ReadFile(filepath.Join(primary, "tracked.txt"))
	if err != nil || string(tracked) != "applied\n" {
		t.Fatalf("tracked=%q error=%v", tracked, err)
	}
	untracked, err := os.ReadFile(filepath.Join(primary, "new.txt"))
	if err != nil || string(untracked) != "new\n" {
		t.Fatalf("untracked=%q error=%v", untracked, err)
	}
	if err := manager.Apply(context.Background(), record, review.PatchHash); !errors.Is(err, repository.ErrPatchConflict) {
		t.Fatalf("second apply error=%v", err)
	}
}

func TestDiscardRequiresMatchingPatchAndRemovesOnlyOwnedWorktree(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(record.Root, "tracked.txt"), []byte("discarded\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	review, err := manager.Review(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Discard(context.Background(), record, strings.Repeat("f", 64)); !errors.Is(err, repository.ErrPatchConflict) {
		t.Fatalf("stale discard error=%v", err)
	}
	if err := manager.Discard(context.Background(), record, review.PatchHash); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(record.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned worktree still exists: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(primary, "tracked.txt"))
	if err != nil || string(content) == "discarded\n" {
		t.Fatalf("primary changed during discard: %q error=%v", content, err)
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

func TestWorktreeSnapshotReadsOnlyChangedPaths(t *testing.T) {
	t.Parallel()
	git, primary, _ := createWorktreeTestRepository(t)
	for index := 0; index < 64; index++ {
		name := filepath.Join(primary, fmt.Sprintf("unchanged-%03d.txt", index))
		if err := os.WriteFile(name, []byte("unchanged\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runGitTest(t, git, primary, "add", ".")
	runGitTest(t, git, primary, "commit", "-m", "add unchanged fixture files")
	baseline := strings.TrimSpace(runGitOutput(t, git, primary, "rev-parse", "HEAD"))
	manager, err := NewWorktreeManager(filepath.Join(t.TempDir(), "talos-owned"))
	if err != nil {
		t.Fatal(err)
	}
	record, err := manager.Create(context.Background(), repository.CreateWorktreeInput{TaskID: testTaskID, RepositoryRoot: primary, BaselineCommit: baseline})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(record.Root, "tracked.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(record.Root, "new.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalSnapshotPath := manager.snapshotPath
	readPaths := make([]string, 0, 2)
	manager.snapshotPath = func(ctx context.Context, root, gitPath string) (string, int64, string, error) {
		readPaths = append(readPaths, gitPath)
		return originalSnapshotPath(ctx, root, gitPath)
	}
	if _, err := manager.Snapshot(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(readPaths, ","), "new.txt,tracked.txt"; got != want {
		t.Fatalf("snapshot read paths=%q want=%q", got, want)
	}
}

func TestSnapshotPathStopsWhenContextIsCanceled(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(strings.Repeat("x", 1<<20)), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := snapshotPath(ctx, root, "large.txt"); !errors.Is(err, repository.ErrWorktreeSnapshotFailed) {
		t.Fatalf("canceled snapshot error=%v", err)
	}
}

func TestWorktreeReviewBoundsTextAndOmitsBinaryContent(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(record.Root, "tracked.txt"), []byte("changed\n"+strings.Repeat("line\n", 20_000)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(record.Root, "untracked.txt"), []byte("token=ghp_abcdefghijklmnopqrstuvwxyz123456\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(record.Root, "binary.dat"), []byte{0, 1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	review, err := manager.Review(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.StateHash) != 64 || len(review.PatchHash) != 64 || len(review.Changes) != 3 || len(review.Diffs) != 3 {
		t.Fatalf("review=%+v", review)
	}
	byPath := make(map[string]repository.FileDiff, len(review.Diffs))
	for _, diff := range review.Diffs {
		byPath[diff.Path] = diff
		if len(diff.Text) > maxDiffBytesPerFile {
			t.Fatalf("unbounded diff for %s: %d", diff.Path, len(diff.Text))
		}
	}
	if !byPath["tracked.txt"].Truncated || byPath["tracked.txt"].Text == "" {
		t.Fatalf("tracked=%+v", byPath["tracked.txt"])
	}
	if byPath["binary.dat"].OmittedReason != "binary" || byPath["binary.dat"].Text != "" {
		t.Fatalf("binary=%+v", byPath["binary.dat"])
	}
	if !strings.Contains(byPath["untracked.txt"].Text, "ghp_abcdefghijklmnopqrstuvwxyz123456") {
		t.Fatalf("untracked adapter diff missing source content: %+v", byPath["untracked.txt"])
	}
}

func TestWorktreeReviewExpandsNestedUntrackedFilesAndMarksLargeContentUnscannable(t *testing.T) {
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
	nested := filepath.Join(record.Root, "internal", "generated")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("safe-content\n", 8_000) + "token=ghp_secret_after_preview_limit\n"
	if err := os.WriteFile(filepath.Join(nested, "large.txt"), []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}

	review, err := manager.Review(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Changes) != 1 || review.Changes[0].Path != "internal/generated/large.txt" {
		t.Fatalf("nested untracked manifest was not expanded: %+v", review.Changes)
	}
	if len(review.Diffs) != 1 || review.Diffs[0].Path != review.Changes[0].Path || !review.Diffs[0].Truncated {
		t.Fatalf("large nested file must be represented and fail closed: %+v", review.Diffs)
	}
	if strings.Contains(review.Diffs[0].Text, "ghp_secret_after_preview_limit") {
		t.Fatal("test fixture secret unexpectedly appeared before the preview limit")
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
