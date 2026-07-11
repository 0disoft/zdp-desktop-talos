package sqliteevent

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
)

func TestTaskAndFirstContractCommitAtomicallyAndSurviveRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "vault.db")
	store := openTestStore(t, path)
	createdAt := time.Unix(1_730_000_000, 0).UTC()
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: "vault-alpha", RetentionDays: 30, OccurredAt: createdAt, IdempotencyKey: "vault-alpha"}); err != nil {
		t.Fatal(err)
	}
	input := taskstore.CreateInput{
		VaultID: "vault-alpha", WorkspaceRoot: `C:\repo`, BaselineCommit: "0123456789012345678901234567890123456789",
		Goal: "Persist immutable contracts", AllowedPaths: []string{"internal/task/**"}, ForbiddenActions: []string{"git.push"},
		AcceptanceCriteria: []string{"restart restores the same contract"}, Risk: task.RiskMedium, OccurredAt: createdAt.Add(time.Minute), IdempotencyKey: "task-alpha",
	}
	created, err := store.CreateTaskContract(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.CreateTaskContract(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Task.ID != created.Task.ID || replayed.Contract.EventID != created.Contract.EventID {
		t.Fatalf("idempotent result changed: %+v %+v", created, replayed)
	}
	if err := store.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	databaseBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(databaseBytes, []byte("Persist immutable contracts")) {
		t.Fatal("event goal leaked as plaintext outside the materialized contract row expectation")
	}

	reopened := openTestStore(t, path)
	defer reopened.Close()
	restoredTask, err := reopened.GetTask(ctx, created.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	restoredContract, err := reopened.GetTaskContract(ctx, created.Task.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if restoredTask.BaselineCommit != input.BaselineCommit || restoredContract.Goal != input.Goal || restoredContract.EventID != created.Contract.EventID {
		t.Fatalf("restored mismatch: %+v %+v", restoredTask, restoredContract)
	}
}

func TestChangedTaskIntentWithSameIdempotencyKeyIsRejected(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	defer store.Close()
	ctx := context.Background()
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: "vault-alpha", RetentionDays: 30, IdempotencyKey: "vault-alpha"}); err != nil {
		t.Fatal(err)
	}
	input := taskstore.CreateInput{VaultID: "vault-alpha", WorkspaceRoot: `C:\repo`, BaselineCommit: "0123456789012345678901234567890123456789", Goal: "first", AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"pass"}, Risk: task.RiskLow, IdempotencyKey: "task-alpha"}
	if _, err := store.CreateTaskContract(ctx, input); err != nil {
		t.Fatal(err)
	}
	input.Goal = "changed"
	if _, err := store.CreateTaskContract(ctx, input); !errors.Is(err, taskstore.ErrIdempotencyConflict) {
		t.Fatalf("error=%v", err)
	}
}

func TestTaskContractFailureRollsBackEventTaskAndIdempotency(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	defer store.Close()
	ctx := context.Background()
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: "vault-alpha", RetentionDays: 30, IdempotencyKey: "vault-alpha"}); err != nil {
		t.Fatal(err)
	}
	beforeEvents := tableCount(t, store, "events")
	if _, err := store.db.Exec(`CREATE TRIGGER reject_contract BEFORE INSERT ON task_contract_revisions BEGIN SELECT RAISE(ABORT, 'forced contract failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := store.CreateTaskContract(ctx, taskstore.CreateInput{VaultID: "vault-alpha", WorkspaceRoot: `C:\repo`, BaselineCommit: "0123456789012345678901234567890123456789", Goal: "fail atomically", AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"pass"}, Risk: task.RiskHigh, IdempotencyKey: "task-alpha"})
	if err == nil {
		t.Fatal("CreateTaskContract succeeded")
	}
	if got := tableCount(t, store, "events"); got != beforeEvents {
		t.Fatalf("events=%d want=%d", got, beforeEvents)
	}
	for _, table := range []string{"tasks", "task_contract_revisions"} {
		if got := tableCount(t, store, table); got != 0 {
			t.Fatalf("%s=%d", table, got)
		}
	}
}

func TestTaskContractBaselineForeignKeyRejectsMutation(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	defer store.Close()
	ctx := context.Background()
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: "vault-alpha", RetentionDays: 30, IdempotencyKey: "vault-alpha"}); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateTaskContract(ctx, taskstore.CreateInput{VaultID: "vault-alpha", WorkspaceRoot: `C:\repo`, BaselineCommit: "0123456789012345678901234567890123456789", Goal: "fixed baseline", AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"pass"}, Risk: task.RiskLow, IdempotencyKey: "task-alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE task_contract_revisions SET baseline_commit = ? WHERE task_id = ?`, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", created.Task.ID); err == nil {
		t.Fatal("baseline mutation unexpectedly succeeded")
	}
}
