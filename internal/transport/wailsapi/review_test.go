package wailsapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/application/patchreview"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/verification"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/worktree"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/repository"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/secretscanner"
)

func TestPatchReviewServiceReturnsOnlySanitizedBoundedDiff(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 1, 0, 0, 0, time.UTC)
	taskID := "019f4a0e-698d-7bb1-86e1-38d4e6889eab"
	baseline := strings.Repeat("a", 40)
	stateHash := strings.Repeat("b", 64)
	store := &patchReviewStoreStub{record: task.Record{ID: taskID, VaultID: "vault-1", WorkspaceRoot: t.TempDir(), BaselineCommit: baseline, Status: task.StatusContracted, CurrentRevision: 1, CreatedAt: now, UpdatedAt: now, LastEventID: "task-event"}, evidence: verification.Evidence{ID: "evidence-1", VaultID: "vault-1", TaskID: taskID, RunID: "run-1", AttemptID: "attempt-1", ContractRevision: 1, CommandIndex: 0, BaselineCommit: baseline, WorktreeStateHash: stateHash, CapabilityHash: strings.Repeat("c", 64), ExitCode: 0, StartedAt: now, FinishedAt: now.Add(time.Second), EventID: "evidence-event"}}
	worktrees := &patchReviewWorktreesStub{record: worktree.Record{TaskID: taskID, RepositoryRoot: store.record.WorkspaceRoot, Root: t.TempDir(), BaselineCommit: baseline, CreatedAt: now}, review: repository.WorktreeReview{StateHash: stateHash, PatchHash: strings.Repeat("d", 64), Diffs: []repository.FileDiff{{Path: "config.txt", Text: "+token=private-value\n", AddedLines: 1}}}}
	core, err := patchreview.New(store, worktrees, patchReviewScannerStub{})
	if err != nil {
		t.Fatal(err)
	}
	vault := openTaskTestVault(t, &serviceDatabase{})
	workspace := openWorkspaceForTest(t, store.record.WorkspaceRoot, baseline)
	service := NewPatchReviewService(vault, workspace, patchReviewFactoryStub{service: core}, nil)
	result := service.GetTaskReview(PatchReviewRequest{TaskID: taskID, CorrelationID: "review-1"})
	if result.Error != nil || result.Review == nil || result.Review.Status != "fresh" || result.Review.PatchHash != strings.Repeat("d", 64) || result.Review.SecretFindings != 1 || len(result.Review.Diffs) != 1 {
		t.Fatalf("result=%+v", result)
	}
	if strings.Contains(result.Review.Diffs[0].Text, "private-value") || !strings.Contains(result.Review.Diffs[0].Text, "[REDACTED]") || strings.Contains(result.Review.Diffs[0].Text, store.record.WorkspaceRoot) {
		t.Fatalf("unsafe diff=%q", result.Review.Diffs[0].Text)
	}
}

func TestPatchReviewRejectsInactiveWorkspaceBeforeOpeningWorktree(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	baseline := strings.Repeat("a", 40)
	record := task.Record{ID: "task-1", VaultID: "vault-1", WorkspaceRoot: t.TempDir(), BaselineCommit: baseline, Status: task.StatusContracted, CurrentRevision: 1, CreatedAt: now, UpdatedAt: now, LastEventID: "event-1"}
	worktrees := &patchReviewWorktreesStub{}
	service, err := patchreview.New(&patchReviewStoreStub{record: record}, worktrees, patchReviewScannerStub{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.GetForWorkspace(context.Background(), record.ID, t.TempDir(), baseline)
	if !errors.Is(err, patchreview.ErrWorkspaceMismatch) || worktrees.opened {
		t.Fatalf("error=%v opened=%v", err, worktrees.opened)
	}
}

type patchReviewFactoryStub struct{ service *patchreview.Service }

func (f patchReviewFactoryStub) NewPatchReview(patchreview.Store) (*patchreview.Service, error) {
	return f.service, nil
}

type patchReviewStoreStub struct {
	record   task.Record
	evidence verification.Evidence
}

func (s *patchReviewStoreStub) GetTask(context.Context, string) (task.Record, error) {
	return s.record, nil
}
func (s *patchReviewStoreStub) GetLatestVerificationEvidence(context.Context, string, string) (verification.Evidence, error) {
	return s.evidence, nil
}

type patchReviewWorktreesStub struct {
	record worktree.Record
	review repository.WorktreeReview
	opened bool
}

func (s *patchReviewWorktreesStub) Open(context.Context, repository.CreateWorktreeInput) (worktree.Record, error) {
	s.opened = true
	return s.record, nil
}
func (s *patchReviewWorktreesStub) Review(context.Context, worktree.Record) (repository.WorktreeReview, error) {
	return s.review, nil
}

type patchReviewScannerStub struct{}

func (patchReviewScannerStub) Redact(_ context.Context, text string) (secretscanner.Result, error) {
	return secretscanner.Result{Text: strings.ReplaceAll(text, "private-value", "[REDACTED]"), Findings: 1}, nil
}
