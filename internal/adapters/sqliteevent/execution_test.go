package sqliteevent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/execution"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/permission"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/executionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
)

func TestPrepareAttemptAtomicallyConsumesOneTimeGrantAndReplays(t *testing.T) {
	t.Parallel()
	store, taskRecord, grant, preparedInput := executionFixture(t)
	defer store.Close()
	ctx := context.Background()
	if _, err := store.SavePermissionGrant(ctx, executionstore.SaveGrantInput{VaultID: taskRecord.VaultID, Grant: grant, OccurredAt: grant.CreatedAt, IdempotencyKey: "grant-create"}); err != nil {
		t.Fatal(err)
	}
	prepared, err := store.PrepareAttempt(ctx, preparedInput)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Run.State != execution.RunActive || prepared.Attempt.State != execution.AttemptDispatchPending || prepared.Attempt.GrantID != grant.ID {
		t.Fatalf("prepared=%+v", prepared)
	}
	replayed, err := store.PrepareAttempt(ctx, preparedInput)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Attempt.ID != prepared.Attempt.ID || replayed.Attempt.LastEventID != prepared.Attempt.LastEventID {
		t.Fatalf("replay changed result: %+v != %+v", replayed, prepared)
	}
	grants, err := store.ListActivePermissionGrants(ctx, taskRecord.VaultID, grant.WorkspaceHash)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 0 {
		t.Fatalf("consumed grant remained active: %+v", grants)
	}
	replayedGrant, err := store.SavePermissionGrant(ctx, executionstore.SaveGrantInput{VaultID: taskRecord.VaultID, Grant: grant, OccurredAt: grant.CreatedAt, IdempotencyKey: "grant-create"})
	if err != nil {
		t.Fatal(err)
	}
	if replayedGrant.State != permission.GrantConsumed {
		t.Fatalf("grant replay lost consumed state: %+v", replayedGrant)
	}
	var state string
	if err := store.db.QueryRow(`SELECT state FROM permission_grants WHERE grant_id = ?`, grant.ID).Scan(&state); err != nil || state != string(permission.GrantConsumed) {
		t.Fatalf("grant state=%q error=%v", state, err)
	}
}

func TestPrepareAttemptRollsBackGrantConsumptionWhenAttemptInsertFails(t *testing.T) {
	t.Parallel()
	store, taskRecord, grant, input := executionFixture(t)
	defer store.Close()
	ctx := context.Background()
	if _, err := store.SavePermissionGrant(ctx, executionstore.SaveGrantInput{VaultID: taskRecord.VaultID, Grant: grant, IdempotencyKey: "grant-create"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_attempt BEFORE INSERT ON attempts BEGIN SELECT RAISE(ABORT, 'forced attempt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareAttempt(ctx, input); err == nil {
		t.Fatal("attempt preparation unexpectedly succeeded")
	}
	var state string
	if err := store.db.QueryRow(`SELECT state FROM permission_grants WHERE grant_id = ?`, grant.ID).Scan(&state); err != nil || state != string(permission.GrantActive) {
		t.Fatalf("grant state=%q error=%v", state, err)
	}
	if count := tableCount(t, store, "runs"); count != 0 {
		t.Fatalf("run survived rollback: %d", count)
	}
}

func TestPrepareAttemptNeverTreatsDenyGrantAsAuthorization(t *testing.T) {
	t.Parallel()
	store, taskRecord, grant, input := executionFixture(t)
	defer store.Close()
	ctx := context.Background()
	grant.Outcome = permission.OutcomeDeny
	if _, err := store.SavePermissionGrant(ctx, executionstore.SaveGrantInput{VaultID: taskRecord.VaultID, Grant: grant, IdempotencyKey: "deny-grant-create"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareAttempt(ctx, input); !errors.Is(err, executionstore.ErrGrantUnavailable) {
		t.Fatalf("deny grant preparation error=%v", err)
	}
	if count := tableCount(t, store, "runs"); count != 0 {
		t.Fatalf("run created from deny grant: %d", count)
	}
	if count := tableCount(t, store, "attempts"); count != 0 {
		t.Fatalf("attempt created from deny grant: %d", count)
	}
}

func TestAttemptFinishAndRestartReconciliationPreserveUnknownOutcome(t *testing.T) {
	t.Parallel()
	store, taskRecord, grant, input := executionFixture(t)
	defer store.Close()
	ctx := context.Background()
	if _, err := store.SavePermissionGrant(ctx, executionstore.SaveGrantInput{VaultID: taskRecord.VaultID, Grant: grant, IdempotencyKey: "grant-create"}); err != nil {
		t.Fatal(err)
	}
	prepared, err := store.PrepareAttempt(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	count, err := store.ReconcilePendingAttempts(ctx, taskRecord.VaultID, input.OccurredAt.Add(time.Minute))
	if err != nil || count != 1 {
		t.Fatalf("reconcile count=%d error=%v", count, err)
	}
	unknown, err := store.GetAttempt(ctx, prepared.Attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unknown.State != execution.AttemptUnknown || unknown.SafeErrorCode != "WORKER_OUTCOME_UNKNOWN" {
		t.Fatalf("unknown=%+v", unknown)
	}
	var runState string
	if err := store.db.QueryRow(`SELECT state FROM runs WHERE run_id = ?`, prepared.Run.ID).Scan(&runState); err != nil || runState != string(execution.RunUnknown) {
		t.Fatalf("run state=%q error=%v", runState, err)
	}
	_, err = store.FinishAttempt(ctx, executionstore.FinishAttemptInput{VaultID: taskRecord.VaultID, AttemptID: prepared.Attempt.ID, ExpectedState: execution.AttemptDispatchPending, NextState: execution.AttemptSucceeded, ExitCode: intPointer(0), IdempotencyKey: "finish-after-unknown"})
	if !errors.Is(err, executionstore.ErrConflict) {
		t.Fatalf("late success error=%v", err)
	}
}

func TestFinishedAttemptAllowsRunToCloseAndReleasesWorkspace(t *testing.T) {
	t.Parallel()
	store, taskRecord, grant, input := executionFixture(t)
	defer store.Close()
	ctx := context.Background()
	if _, err := store.SavePermissionGrant(ctx, executionstore.SaveGrantInput{VaultID: taskRecord.VaultID, Grant: grant, IdempotencyKey: "grant-create"}); err != nil {
		t.Fatal(err)
	}
	prepared, err := store.PrepareAttempt(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishAttempt(ctx, executionstore.FinishAttemptInput{VaultID: taskRecord.VaultID, AttemptID: prepared.Attempt.ID, ExpectedState: execution.AttemptDispatchPending, NextState: execution.AttemptSucceeded, ExitCode: intPointer(0), OccurredAt: input.OccurredAt.Add(time.Second), IdempotencyKey: "finish-attempt"}); err != nil {
		t.Fatal(err)
	}
	finished, err := store.FinishRun(ctx, executionstore.FinishRunInput{VaultID: taskRecord.VaultID, RunID: prepared.Run.ID, ExpectedState: execution.RunActive, NextState: execution.RunCompleted, OccurredAt: input.OccurredAt.Add(2 * time.Second), IdempotencyKey: "finish-run"})
	if err != nil {
		t.Fatal(err)
	}
	if finished.State != execution.RunCompleted {
		t.Fatalf("finished=%+v", finished)
	}
	replayed, err := store.PrepareAttempt(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Run.State != execution.RunCompleted || replayed.Attempt.State != execution.AttemptSucceeded {
		t.Fatalf("late preparation replay lost current state: %+v", replayed)
	}
	second := input
	second.RunID = "run-2"
	second.AttemptID = "attempt-2"
	second.CallID = "call-2"
	second.GrantID = ""
	second.IdempotencyKey = "prepare-second-run"
	if _, err := store.PrepareAttempt(ctx, second); err != nil {
		t.Fatalf("workspace was not released: %v", err)
	}
}

func executionFixture(t *testing.T) (*Store, task.Record, permission.Grant, executionstore.PrepareAttemptInput) {
	t.Helper()
	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	ctx := context.Background()
	now := time.Unix(1_752_230_400, 0).UTC()
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: "vault-execution", RetentionDays: 30, OccurredAt: now, IdempotencyKey: "vault-create"}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "Repository")
	created, err := store.CreateTaskContract(ctx, taskstore.CreateInput{VaultID: "vault-execution", WorkspaceRoot: root, BaselineCommit: strings.Repeat("a", 40), Goal: "journal execution", AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"journaled"}, Risk: task.RiskHigh, OccurredAt: now.Add(time.Second), IdempotencyKey: "task-create"})
	if err != nil {
		t.Fatal(err)
	}
	workspaceHash, err := permission.WorkspaceHash(root)
	if err != nil {
		t.Fatal(err)
	}
	capabilityHash := strings.Repeat("b", 64)
	grant := permission.Grant{ID: "grant-once", Outcome: permission.OutcomeAllowOnce, State: permission.GrantActive, CapabilityHash: capabilityHash, TaskID: created.Task.ID, WorkspaceHash: workspaceHash, CreatedAt: now.Add(2 * time.Second), ExpiresAt: now.Add(time.Hour)}
	input := executionstore.PrepareAttemptInput{VaultID: created.Task.VaultID, TaskID: created.Task.ID, WorkspaceHash: workspaceHash, RunID: "run-1", AttemptID: "attempt-1", CallID: "call-1", CapabilityHash: capabilityHash, GrantID: grant.ID, OccurredAt: now.Add(3 * time.Second), IdempotencyKey: "prepare-attempt"}
	return store, created.Task, grant, input
}

func intPointer(value int) *int { return &value }
