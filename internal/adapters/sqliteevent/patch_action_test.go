package sqliteevent

import (
	"context"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/execution"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/patchaction"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/executionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/patchstore"
)

func TestPatchApplyJournalClosesTaskAtomicallyAndReplays(t *testing.T) {
	t.Parallel()
	store, taskRecord, grant, attemptInput := executionFixture(t)
	defer store.Close()
	ctx := context.Background()
	if _, err := store.SavePermissionGrant(ctx, executionstore.SaveGrantInput{VaultID: taskRecord.VaultID, Grant: grant, IdempotencyKey: "patch-grant"}); err != nil {
		t.Fatal(err)
	}
	preparedAttempt, err := store.PrepareAttempt(ctx, attemptInput)
	if err != nil {
		t.Fatal(err)
	}
	finishedAttempt, err := store.FinishAttempt(ctx, executionstore.FinishAttemptInput{VaultID: taskRecord.VaultID, AttemptID: preparedAttempt.Attempt.ID, ExpectedState: execution.AttemptDispatchPending, NextState: execution.AttemptSucceeded, ExitCode: intPointer(0), OccurredAt: attemptInput.OccurredAt.Add(time.Second), IdempotencyKey: "patch-evidence", Evidence: evidenceInput(taskRecord, preparedAttempt, attemptInput.OccurredAt, attemptInput.OccurredAt.Add(time.Second))})
	if err != nil || finishedAttempt.Evidence == nil {
		t.Fatalf("finish=%+v error=%v", finishedAttempt, err)
	}
	input := patchstore.PrepareInput{VaultID: taskRecord.VaultID, TaskID: taskRecord.ID, Kind: patchaction.KindApply, ContractRevision: taskRecord.CurrentRevision, PatchHash: repeatHash("d"), WorktreeStateHash: finishedAttempt.Evidence.WorktreeStateHash, EvidenceID: finishedAttempt.Evidence.ID, OccurredAt: attemptInput.OccurredAt.Add(2 * time.Second), IdempotencyKey: "patch-apply"}
	prepared, err := store.PreparePatchAction(ctx, input)
	if err != nil || prepared.Action.State != patchaction.StatePending || prepared.Replayed {
		t.Fatalf("prepared=%+v error=%v", prepared, err)
	}
	replayed, err := store.PreparePatchAction(ctx, input)
	if err != nil || !replayed.Replayed || replayed.Action.ID != prepared.Action.ID {
		t.Fatalf("replayed=%+v error=%v", replayed, err)
	}
	finished, err := store.FinishPatchAction(ctx, patchstore.FinishInput{VaultID: taskRecord.VaultID, ActionID: prepared.Action.ID, ExpectedState: patchaction.StatePending, NextState: patchaction.StateSucceeded, OccurredAt: attemptInput.OccurredAt.Add(3 * time.Second), IdempotencyKey: "patch-apply:finish"})
	if err != nil || finished.State != patchaction.StateSucceeded {
		t.Fatalf("finished=%+v error=%v", finished, err)
	}
	current, err := store.GetTask(ctx, taskRecord.ID)
	if err != nil || current.Status != task.StatusCompleted {
		t.Fatalf("task=%+v error=%v", current, err)
	}
	var outcomes int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM task_outcomes WHERE task_id = ? AND action_id = ?`, taskRecord.ID, prepared.Action.ID).Scan(&outcomes); err != nil || outcomes != 1 {
		t.Fatalf("outcomes=%d error=%v", outcomes, err)
	}
}

func TestDiscardJournalDoesNotRequireEvidence(t *testing.T) {
	t.Parallel()
	store, taskRecord, _, attemptInput := executionFixture(t)
	defer store.Close()
	ctx := context.Background()
	prepared, err := store.PreparePatchAction(ctx, patchstore.PrepareInput{VaultID: taskRecord.VaultID, TaskID: taskRecord.ID, Kind: patchaction.KindDiscard, ContractRevision: taskRecord.CurrentRevision, PatchHash: repeatHash("e"), WorktreeStateHash: repeatHash("f"), OccurredAt: attemptInput.OccurredAt, IdempotencyKey: "patch-discard"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishPatchAction(ctx, patchstore.FinishInput{VaultID: taskRecord.VaultID, ActionID: prepared.Action.ID, ExpectedState: patchaction.StatePending, NextState: patchaction.StateSucceeded, OccurredAt: attemptInput.OccurredAt.Add(time.Second), IdempotencyKey: "patch-discard:finish"}); err != nil {
		t.Fatal(err)
	}
	current, err := store.GetTask(ctx, taskRecord.ID)
	if err != nil || current.Status != task.StatusDiscarded {
		t.Fatalf("task=%+v error=%v", current, err)
	}
}

func repeatHash(value string) string {
	result := ""
	for len(result) < 64 {
		result += value
	}
	return result[:64]
}
