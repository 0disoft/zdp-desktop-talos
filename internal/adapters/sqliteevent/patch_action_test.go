package sqliteevent

import (
	"context"
	"errors"
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
	replayInput := patchstore.ReplayInput{VaultID: input.VaultID, TaskID: input.TaskID, Kind: input.Kind, ContractRevision: input.ContractRevision, PatchHash: input.PatchHash, IdempotencyKey: input.IdempotencyKey}
	prior, err := store.FindPatchAction(ctx, replayInput)
	if err != nil || prior.ID != prepared.Action.ID || prior.State != patchaction.StateSucceeded {
		t.Fatalf("saved action=%+v error=%v", prior, err)
	}
	for _, change := range []func(*patchstore.ReplayInput){
		func(in *patchstore.ReplayInput) { in.VaultID = "different-vault" },
		func(in *patchstore.ReplayInput) { in.TaskID = "different-task" },
		func(in *patchstore.ReplayInput) { in.Kind = patchaction.KindDiscard },
		func(in *patchstore.ReplayInput) { in.ContractRevision++ },
		func(in *patchstore.ReplayInput) { in.PatchHash = repeatHash("f") },
		func(in *patchstore.ReplayInput) { in.IdempotencyKey = "patch-evidence" },
	} {
		changed := replayInput
		change(&changed)
		if _, err := store.FindPatchAction(ctx, changed); !errors.Is(err, patchstore.ErrIdempotencyConflict) {
			t.Fatalf("changed replay accepted: %+v error=%v", changed, err)
		}
	}
	replayInput.IdempotencyKey = "new-patch-command"
	if _, err := store.FindPatchAction(ctx, replayInput); !errors.Is(err, patchstore.ErrNotFound) {
		t.Fatalf("new request error=%v", err)
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
	replayed, err := store.FindPatchAction(ctx, patchstore.ReplayInput{VaultID: taskRecord.VaultID, TaskID: taskRecord.ID, Kind: patchaction.KindDiscard, ContractRevision: taskRecord.CurrentRevision, PatchHash: repeatHash("e"), IdempotencyKey: "patch-discard"})
	if err != nil || replayed.ID != prepared.Action.ID || replayed.State != patchaction.StateSucceeded {
		t.Fatalf("discard replay=%+v error=%v", replayed, err)
	}
}

func repeatHash(value string) string {
	result := ""
	for len(result) < 64 {
		result += value
	}
	return result[:64]
}
