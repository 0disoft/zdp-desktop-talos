package gitcli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0disoft/zdp-desktop-talos/internal/ports/repository"
)

type maliciousRepositoryCorpus struct {
	Schema              string                     `json:"schema"`
	AutomatedCases      []maliciousRepositoryCase  `json:"automated_cases"`
	DeclaredLimitations []maliciousRepositoryLimit `json:"declared_limitations"`
}

type maliciousRepositoryCase struct {
	ID       string `json:"id"`
	Boundary string `json:"boundary"`
	Expected string `json:"expected"`
}

type maliciousRepositoryLimit struct {
	ID     string `json:"id"`
	Owner  string `json:"owner"`
	Reason string `json:"reason"`
}

func TestMaliciousRepositoryCorpus(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("testdata", "malicious-repositories-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var corpus maliciousRepositoryCorpus
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&corpus); err != nil || corpus.Schema != "talos.malicious-repository-corpus/1" {
		t.Fatalf("invalid corpus schema: %q error=%v", corpus.Schema, err)
	}
	automated := map[string]func(*testing.T){
		"outside-symlink":          exerciseOutsideSymlink,
		"external-diff-helper":     exerciseExternalDiffHelper,
		"hostile-checkout-hook":    exerciseHostileCheckoutHook,
		"oversized-untracked-file": exerciseOversizedUntrackedFile,
		"dirty-primary-apply":      exerciseDirtyPrimaryApply,
		"detached-head":            exerciseDetachedHead,
	}
	seen := make(map[string]struct{}, len(corpus.AutomatedCases))
	for _, testCase := range corpus.AutomatedCases {
		exercise, exists := automated[testCase.ID]
		if !exists || testCase.Boundary == "" || testCase.Expected == "" {
			t.Fatalf("unimplemented or incomplete automated case: %+v", testCase)
		}
		if _, duplicate := seen[testCase.ID]; duplicate {
			t.Fatalf("duplicate automated case %q", testCase.ID)
		}
		seen[testCase.ID] = struct{}{}
		t.Run(testCase.ID, exercise)
	}
	if len(seen) != len(automated) {
		t.Fatalf("corpus omitted automated cases: got=%d want=%d", len(seen), len(automated))
	}

	wantedLimits := map[string]string{
		"home-directory-read":             "platform-os-sandbox",
		"case-collision":                  "native-filesystem-matrix",
		"unicode-normalization-collision": "native-filesystem-matrix",
		"endless-child-process":           "native-worker-containment",
	}
	seenLimits := make(map[string]struct{}, len(corpus.DeclaredLimitations))
	for _, limitation := range corpus.DeclaredLimitations {
		owner, exists := wantedLimits[limitation.ID]
		if !exists || limitation.Owner != owner || strings.TrimSpace(limitation.Reason) == "" {
			t.Fatalf("unowned or incomplete limitation: %+v", limitation)
		}
		if _, duplicate := seenLimits[limitation.ID]; duplicate {
			t.Fatalf("duplicate limitation %q", limitation.ID)
		}
		seenLimits[limitation.ID] = struct{}{}
	}
	if len(seenLimits) != len(wantedLimits) {
		t.Fatalf("corpus omitted declared limitations: got=%d want=%d", len(seenLimits), len(wantedLimits))
	}
}

func exerciseOutsideSymlink(t *testing.T) {
	_, primary, baseline := createWorktreeTestRepository(t)
	manager := newCorpusWorktreeManager(t)
	record, err := manager.Create(context.Background(), repository.CreateWorktreeInput{TaskID: corpusTaskID(t.Name()), RepositoryRoot: primary, BaselineCommit: baseline})
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside-secret.txt")
	if err := os.WriteFile(outside, []byte("must-not-cross-boundary\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(record.Root, "outside-link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("native symlink creation unavailable: %v", err)
	}
	review, err := manager.Review(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	diff := corpusDiff(t, review, "outside-link.txt")
	if diff.OmittedReason != "symlink" || diff.Text != "" || strings.Contains(diff.Text, "must-not-cross-boundary") {
		t.Fatalf("outside symlink was not omitted: %+v", diff)
	}
}

func exerciseExternalDiffHelper(t *testing.T) {
	git, primary, _ := createWorktreeTestRepository(t)
	if err := os.WriteFile(filepath.Join(primary, ".gitattributes"), []byte("tracked.txt diff=hostile\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, git, primary, "add", ".gitattributes")
	runGitTest(t, git, primary, "commit", "-m", "add hostile diff attribute")
	baseline := strings.TrimSpace(runGitOutput(t, git, primary, "rev-parse", "HEAD"))
	runGitTest(t, git, primary, "config", "diff.hostile.command", filepath.Join(t.TempDir(), "must-not-run-helper"))
	manager := newCorpusWorktreeManager(t)
	record, err := manager.Create(context.Background(), repository.CreateWorktreeInput{TaskID: corpusTaskID(t.Name()), RepositoryRoot: primary, BaselineCommit: baseline})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(record.Root, "tracked.txt"), []byte("safe adapter diff\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	review, err := manager.Review(context.Background(), record)
	if err != nil {
		t.Fatalf("repository diff helper influenced review: %v", err)
	}
	diff := corpusDiff(t, review, "tracked.txt")
	if !strings.Contains(diff.Text, "+safe adapter diff") {
		t.Fatalf("trusted built-in diff missing: %+v", diff)
	}
}

func exerciseHostileCheckoutHook(t *testing.T) {
	git, primary, baseline := createWorktreeTestRepository(t)
	hooks := filepath.Join(t.TempDir(), "hostile-hooks")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(hooks, "post-checkout")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 99\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, git, primary, "config", "core.hooksPath", hooks)
	manager := newCorpusWorktreeManager(t)
	if _, err := manager.Create(context.Background(), repository.CreateWorktreeInput{TaskID: corpusTaskID(t.Name()), RepositoryRoot: primary, BaselineCommit: baseline}); err != nil {
		t.Fatalf("repository hook influenced worktree creation: %v", err)
	}
}

func exerciseOversizedUntrackedFile(t *testing.T) {
	_, primary, baseline := createWorktreeTestRepository(t)
	manager := newCorpusWorktreeManager(t)
	record, err := manager.Create(context.Background(), repository.CreateWorktreeInput{TaskID: corpusTaskID(t.Name()), RepositoryRoot: primary, BaselineCommit: baseline})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(record.Root, "oversized.txt"), []byte(strings.Repeat("payload-line\n", maxDiffBytesPerFile/4)), 0o600); err != nil {
		t.Fatal(err)
	}
	review, err := manager.Review(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	diff := corpusDiff(t, review, "oversized.txt")
	if !diff.Truncated || len(diff.Text) > maxDiffBytesPerFile {
		t.Fatalf("oversized diff was not bounded: bytes=%d truncated=%v", len(diff.Text), diff.Truncated)
	}
}

func exerciseDirtyPrimaryApply(t *testing.T) {
	_, primary, baseline := createWorktreeTestRepository(t)
	manager := newCorpusWorktreeManager(t)
	record, err := manager.Create(context.Background(), repository.CreateWorktreeInput{TaskID: corpusTaskID(t.Name()), RepositoryRoot: primary, BaselineCommit: baseline})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(record.Root, "tracked.txt"), []byte("task patch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	review, err := manager.Review(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(primary, "tracked.txt"), []byte("user edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(context.Background(), record, review.PatchHash); !errors.Is(err, repository.ErrPatchConflict) {
		t.Fatalf("dirty primary apply error=%v", err)
	}
}

func exerciseDetachedHead(t *testing.T) {
	git, primary, _ := createWorktreeTestRepository(t)
	runGitTest(t, git, primary, "checkout", "--detach")
	inspector, err := New()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := inspector.Inspect(context.Background(), primary)
	if err != nil || !snapshot.Detached || snapshot.HeadRef != "" {
		t.Fatalf("snapshot=%+v error=%v", snapshot, err)
	}
}

func newCorpusWorktreeManager(t *testing.T) *WorktreeManager {
	t.Helper()
	manager, err := NewWorktreeManager(filepath.Join(t.TempDir(), "talos-owned"))
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func corpusDiff(t *testing.T, review repository.WorktreeReview, path string) repository.FileDiff {
	t.Helper()
	for _, diff := range review.Diffs {
		if diff.Path == path {
			return diff
		}
	}
	t.Fatalf("missing corpus diff for %q: %+v", path, review.Diffs)
	return repository.FileDiff{}
}

func corpusTaskID(name string) string {
	var suffix uint64
	for _, character := range name {
		suffix = suffix*131 + uint64(character)
	}
	return "01900000-0000-7000-8000-" + leftPadDecimal(suffix%1_000_000_000_000, 12)
}

func leftPadDecimal(value uint64, width int) string {
	digits := "0123456789"
	result := make([]byte, width)
	for index := width - 1; index >= 0; index-- {
		result[index] = digits[value%10]
		value /= 10
	}
	return string(result)
}
