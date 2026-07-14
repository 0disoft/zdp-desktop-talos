package patchreview

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/verification"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspace"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/worktree"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/executionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/repository"
)

func TestGetClassifiesFreshStaleAndUnverifiedReview(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 14, 6, 0, 0, 0, time.UTC)
	record := task.Record{ID: "019f4a0e-698d-7bb1-86e1-38d4e6889eab", VaultID: "vault-1", WorkspaceRoot: t.TempDir(), BaselineCommit: strings.Repeat("a", 40), Status: task.StatusContracted, CurrentRevision: 2, CreatedAt: now, UpdatedAt: now, LastEventID: "event-1"}
	hash := strings.Repeat("b", 64)
	evidence := verification.Evidence{ID: "evidence-1", VaultID: record.VaultID, TaskID: record.ID, RunID: "run-1", AttemptID: "attempt-1", ContractRevision: 2, CommandIndex: 0, BaselineCommit: record.BaselineCommit, WorktreeStateHash: hash, CapabilityHash: strings.Repeat("c", 64), ExitCode: 0, StartedAt: now, FinishedAt: now.Add(time.Second), EventID: "event-2"}
	store := &reviewStore{task: record, evidence: evidence}
	worktrees := &reviewWorktrees{record: worktree.Record{TaskID: record.ID, RepositoryRoot: record.WorkspaceRoot, Root: t.TempDir(), BaselineCommit: record.BaselineCommit, CreatedAt: now}, review: repository.WorktreeReview{StateHash: hash, Changes: []workspace.Change{{Path: "main.go", Kind: workspace.ChangeTracked, IndexStatus: '.', WorktreeStatus: 'M'}}}}
	service, err := New(store, worktrees)
	if err != nil {
		t.Fatal(err)
	}

	fresh, err := service.Get(context.Background(), record.ID)
	if err != nil || fresh.Status != StatusFresh || fresh.Reason != "fresh" || len(fresh.Changes) != 1 {
		t.Fatalf("fresh=%+v err=%v", fresh, err)
	}

	worktrees.review.StateHash = strings.Repeat("d", 64)
	stale, err := service.Get(context.Background(), record.ID)
	if err != nil || stale.Status != StatusStale || stale.Reason != "patch_changed" {
		t.Fatalf("stale=%+v err=%v", stale, err)
	}

	store.err = executionstore.ErrNotFound
	unverified, err := service.Get(context.Background(), record.ID)
	if err != nil || unverified.Status != StatusUnverified || unverified.Evidence != nil || unverified.Reason != "no_evidence" {
		t.Fatalf("unverified=%+v err=%v", unverified, err)
	}
}

func TestGetFailsClosedWhenOwnedWorktreeCannotBeReviewed(t *testing.T) {
	t.Parallel()
	service, err := New(&reviewStore{task: task.Record{ID: "task-1"}}, &reviewWorktrees{err: repository.ErrWorktreeOwnership})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Get(context.Background(), "task-1")
	if !errors.Is(err, repository.ErrWorktreeOwnership) {
		t.Fatalf("err=%v", err)
	}
}

type reviewStore struct {
	task     task.Record
	evidence verification.Evidence
	err      error
}

func (s *reviewStore) GetTask(context.Context, string) (task.Record, error) { return s.task, nil }
func (s *reviewStore) GetLatestVerificationEvidence(context.Context, string, string) (verification.Evidence, error) {
	return s.evidence, s.err
}

type reviewWorktrees struct {
	record worktree.Record
	review repository.WorktreeReview
	err    error
}

func (w *reviewWorktrees) Open(context.Context, repository.CreateWorktreeInput) (worktree.Record, error) {
	return w.record, w.err
}
func (w *reviewWorktrees) Review(context.Context, worktree.Record) (repository.WorktreeReview, error) {
	return w.review, w.err
}
