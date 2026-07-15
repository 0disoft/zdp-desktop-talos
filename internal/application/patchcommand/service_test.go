package patchcommand

import (
	"context"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/application/patchreview"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/patchaction"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/verification"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/worktree"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/patchstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/repository"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
)

func TestExecuteJournalsBeforeMutationAndCompletesTask(t *testing.T) {
	t.Parallel()
	record, contract, review, request := gateFixture()
	store := &commandStore{record: record, contract: contract}
	worktrees := &commandWorktrees{record: worktree.Record{TaskID: record.ID, RepositoryRoot: record.WorkspaceRoot, Root: `C:\owned`, BaselineCommit: record.BaselineCommit, CreatedAt: record.CreatedAt}}
	service, err := New(store, commandReviewer{review: review}, worktrees)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if worktrees.applied != 1 || !store.preparedBeforeMutation || result.Action.State != patchaction.StateSucceeded || result.TaskStatus != task.StatusCompleted {
		t.Fatalf("result=%+v worktrees=%+v store=%+v", result, worktrees, store)
	}
}

func TestExecuteNeverReplaysPendingExternalMutation(t *testing.T) {
	t.Parallel()
	record, contract, review, request := gateFixture()
	store := &commandStore{record: record, contract: contract, replay: true}
	worktrees := &commandWorktrees{record: worktree.Record{TaskID: record.ID, RepositoryRoot: record.WorkspaceRoot, Root: `C:\owned`, BaselineCommit: record.BaselineCommit, CreatedAt: record.CreatedAt}}
	service, err := New(store, commandReviewer{review: review}, worktrees)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Execute(context.Background(), request)
	if err != ErrActionUnresolved || !result.Replayed || worktrees.applied != 0 {
		t.Fatalf("result=%+v error=%v applied=%d", result, err, worktrees.applied)
	}
}

type commandReviewer struct{ review patchreview.Result }

func (r commandReviewer) Get(context.Context, string) (patchreview.Result, error) {
	return r.review, nil
}

type commandStore struct {
	record                 task.Record
	contract               task.ContractRevision
	replay                 bool
	preparedBeforeMutation bool
}

func (*commandStore) CreateTaskContract(context.Context, taskstore.CreateInput) (taskstore.Created, error) {
	return taskstore.Created{}, taskstore.ErrInvalidCommand
}
func (*commandStore) ReviseTaskContract(context.Context, taskstore.ReviseInput) (taskstore.Created, error) {
	return taskstore.Created{}, taskstore.ErrInvalidCommand
}
func (s *commandStore) GetTask(context.Context, string) (task.Record, error) { return s.record, nil }
func (s *commandStore) GetTaskContract(context.Context, string, int) (task.ContractRevision, error) {
	return s.contract, nil
}
func (*commandStore) GetLatestVerificationEvidence(context.Context, string, string) (verification.Evidence, error) {
	return verification.Evidence{}, nil
}
func (s *commandStore) PreparePatchAction(_ context.Context, input patchstore.PrepareInput) (patchstore.Prepared, error) {
	s.preparedBeforeMutation = true
	now := time.Date(2026, 7, 15, 2, 0, 0, 0, time.UTC)
	action := patchaction.Record{ID: "action-1", VaultID: input.VaultID, TaskID: input.TaskID, Kind: input.Kind, State: patchaction.StatePending, ContractRevision: input.ContractRevision, PatchHash: input.PatchHash, WorktreeStateHash: input.WorktreeStateHash, EvidenceID: input.EvidenceID, CreatedAt: now, UpdatedAt: now, CreatedEventID: "event-1", LastEventID: "event-1"}
	return patchstore.Prepared{Action: action, Replayed: s.replay}, nil
}
func (*commandStore) FinishPatchAction(_ context.Context, input patchstore.FinishInput) (patchaction.Record, error) {
	now := time.Date(2026, 7, 15, 2, 0, 0, 0, time.UTC)
	return patchaction.Record{ID: input.ActionID, VaultID: "vault-1", TaskID: "task-1", Kind: patchaction.KindApply, State: input.NextState, ContractRevision: 2, PatchHash: repeatCommand("c"), WorktreeStateHash: repeatCommand("b"), EvidenceID: "evidence-1", SafeErrorCode: input.SafeErrorCode, CreatedAt: now, UpdatedAt: now, CreatedEventID: "event-1", LastEventID: "event-2"}, nil
}

type commandWorktrees struct {
	record  worktree.Record
	applied int
}

func (w *commandWorktrees) Open(context.Context, repository.CreateWorktreeInput) (worktree.Record, error) {
	return w.record, nil
}
func (w *commandWorktrees) Apply(context.Context, worktree.Record, string) error {
	w.applied++
	return nil
}
func (*commandWorktrees) Discard(context.Context, worktree.Record, string) error { return nil }

func repeatCommand(value string) string {
	result := ""
	for len(result) < 64 {
		result += value
	}
	return result[:64]
}
