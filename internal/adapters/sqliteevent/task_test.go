package sqliteevent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

func TestTaskContractRevisionIsAtomicRevisionCheckedAndReplayable(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	defer store.Close()
	ctx := context.Background()
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: "vault-alpha", RetentionDays: 30, IdempotencyKey: "vault-alpha"}); err != nil {
		t.Fatal(err)
	}
	createInput := taskstore.CreateInput{VaultID: "vault-alpha", WorkspaceRoot: `C:\repo`, BaselineCommit: "0123456789012345678901234567890123456789", Goal: "revision one", AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"one"}, Risk: task.RiskLow, IdempotencyKey: "task-create"}
	created, err := store.CreateTaskContract(ctx, createInput)
	if err != nil {
		t.Fatal(err)
	}
	revised, err := store.ReviseTaskContract(ctx, taskstore.ReviseInput{VaultID: "vault-alpha", TaskID: created.Task.ID, ExpectedRevision: 1, Goal: "revision two", AllowedPaths: []string{"internal/**", "frontend/**"}, ForbiddenActions: []string{"git.push"}, AcceptanceCriteria: []string{"two"}, Risk: task.RiskMedium, IdempotencyKey: "task-revise-2"})
	if err != nil {
		t.Fatal(err)
	}
	if revised.Task.CurrentRevision != 2 || revised.Contract.Revision != 2 || revised.Task.BaselineCommit != created.Task.BaselineCommit || revised.Contract.Goal != "revision two" {
		t.Fatalf("revised=%+v", revised)
	}
	if _, err := store.ReviseTaskContract(ctx, taskstore.ReviseInput{VaultID: "vault-alpha", TaskID: created.Task.ID, ExpectedRevision: 1, Goal: "stale", AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"stale"}, Risk: task.RiskLow, IdempotencyKey: "task-stale"}); !errors.Is(err, taskstore.ErrRevisionConflict) {
		t.Fatalf("stale error=%v", err)
	}
	if got := tableCount(t, store, "task_contract_revisions"); got != 2 {
		t.Fatalf("revision rows=%d", got)
	}
	replayedRevision, err := store.ReviseTaskContract(ctx, taskstore.ReviseInput{VaultID: "vault-alpha", TaskID: created.Task.ID, ExpectedRevision: 1, Goal: "revision two", AllowedPaths: []string{"internal/**", "frontend/**"}, ForbiddenActions: []string{"git.push"}, AcceptanceCriteria: []string{"two"}, Risk: task.RiskMedium, IdempotencyKey: "task-revise-2"})
	if err != nil || replayedRevision.Contract.EventID != revised.Contract.EventID || replayedRevision.Task.CurrentRevision != 2 {
		t.Fatalf("replayed revision=%+v err=%v", replayedRevision, err)
	}
	replayedCreate, err := store.CreateTaskContract(ctx, createInput)
	if err != nil || replayedCreate.Contract.Revision != 1 || replayedCreate.Contract.EventID != created.Contract.EventID {
		t.Fatalf("replayed create=%+v err=%v", replayedCreate, err)
	}
	current, err := store.GetTask(ctx, created.Task.ID)
	if err != nil || current.CurrentRevision != 2 || current.LastEventID != revised.Contract.EventID {
		t.Fatalf("current=%+v err=%v", current, err)
	}
}

func TestConcurrentTaskRevisionsAllowOneWinner(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	defer store.Close()
	ctx := context.Background()
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: "vault-alpha", RetentionDays: 30, IdempotencyKey: "vault-alpha"}); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateTaskContract(ctx, taskstore.CreateInput{VaultID: "vault-alpha", WorkspaceRoot: `C:\repo`, BaselineCommit: "0123456789012345678901234567890123456789", Goal: "one", AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"one"}, Risk: task.RiskLow, IdempotencyKey: "task-create"})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for index := 0; index < 2; index++ {
		index := index
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := store.ReviseTaskContract(ctx, taskstore.ReviseInput{VaultID: "vault-alpha", TaskID: created.Task.ID, ExpectedRevision: 1, Goal: fmt.Sprintf("winner-%d", index), AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"pass"}, Risk: task.RiskMedium, IdempotencyKey: fmt.Sprintf("revise-%d", index)})
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, taskstore.ErrRevisionConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected error=%v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
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
