package sqliteevent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/planning"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/taskbudget"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/modelstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
)

func TestModelEgressReceiptLifecycleIsIdempotentAndContainsNoRawPrompt(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	defer store.Close()
	ctx := context.Background()
	now := time.Date(2026, 7, 16, 2, 0, 0, 0, time.UTC)
	vaultID := "vault-model-egress"
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: vaultID, RetentionDays: 30, OccurredAt: now.Add(-time.Minute), IdempotencyKey: "vault-create"}); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateTaskContract(ctx, taskstore.CreateInput{
		VaultID: vaultID, WorkspaceRoot: filepath.Join(t.TempDir(), "repo"), BaselineCommit: strings.Repeat("a", 40),
		Goal: "plan safely", AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"tests pass"},
		VerificationCommands: []task.VerificationCommand{{RuleID: "go-test", Arguments: []string{"test", "./..."}, WorkingDirectory: "."}},
		Risk:                 task.RiskLow, OccurredAt: now.Add(-time.Second), IdempotencyKey: "task-create",
	})
	if err != nil {
		t.Fatal(err)
	}
	prepare := modelstore.PrepareInput{
		VaultID: vaultID, TaskID: created.Task.ID, ContractRevision: 1, ProviderKey: "fixture", ModelKey: "fixture-plan-v1",
		RequestID: "request-1", PromptVersion: "planning.v1", ContextHash: strings.Repeat("b", 64), RequestHash: strings.Repeat("c", 64),
		ContextItems: 2, InputBytes: 512, RedactionCount: 1, ReservedInputTokens: 512, ReservedOutputTokens: 1024, OccurredAt: now, IdempotencyKey: "egress-prepare",
	}
	receipt, err := store.PrepareModelEgress(ctx, prepare)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.PrepareModelEgress(ctx, prepare)
	if err != nil || replayed.ID != receipt.ID {
		t.Fatalf("replayed=%+v error=%v", replayed, err)
	}
	var modelCalls int
	if err := store.db.QueryRow(`SELECT model_calls FROM task_budget_counters WHERE task_id = ?`, created.Task.ID).Scan(&modelCalls); err != nil || modelCalls != 1 {
		t.Fatalf("model calls=%d error=%v", modelCalls, err)
	}
	finish := modelstore.FinishInput{
		VaultID: vaultID, ReceiptID: receipt.ID, ExpectedStatus: planning.EgressPrepared, NextStatus: planning.EgressCompleted,
		ResponseHash: strings.Repeat("d", 64), ProviderCallID: "fixture-call", OutputBytes: 128, Usage: planning.Usage{InputTokens: 20, CachedInputTokens: 5, OutputTokens: 10},
		OccurredAt: now.Add(time.Second), IdempotencyKey: "egress-finish",
	}
	completed, err := store.FinishModelEgress(ctx, finish)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != planning.EgressCompleted || completed.Usage.CachedInputTokens != 5 {
		t.Fatalf("completed=%+v", completed)
	}
	if count := tableCount(t, store, "model_egress_receipts"); count != 1 {
		t.Fatalf("receipt rows=%d", count)
	}
	var plaintextCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM events WHERE CAST(payload_envelope AS TEXT) LIKE '%plan safely%'`).Scan(&plaintextCount); err != nil || plaintextCount != 0 {
		t.Fatalf("raw prompt appeared in event storage: count=%d error=%v", plaintextCount, err)
	}
}

func TestModelBudgetReservationIsAtomicAndIdempotent(t *testing.T) {
	store, _, _ := modelEgressFixture(t)
	defer store.Close()
	ctx := context.Background()
	policy := taskbudget.Policy{MaxModelCalls: 1, MaxToolCalls: 4, MaxInputTokens: 4096, MaxOutputTokens: 2048, MaxWallClock: time.Hour}
	var taskID, vaultID string
	if err := store.db.QueryRow(`SELECT task_id, vault_id FROM tasks LIMIT 1`).Scan(&taskID, &vaultID); err != nil {
		t.Fatal(err)
	}
	input := modelstore.PrepareInput{VaultID: vaultID, TaskID: taskID, ContractRevision: 1, ProviderKey: "fixture", ModelKey: "fixture-plan-v1", RequestID: "budget-request", PromptVersion: "planning.v1", ContextHash: strings.Repeat("e", 64), RequestHash: strings.Repeat("f", 64), ContextItems: 1, InputBytes: 256, ReservedInputTokens: 256, ReservedOutputTokens: 512, Budget: policy, OccurredAt: time.Date(2026, 7, 16, 3, 1, 0, 0, time.UTC), IdempotencyKey: "budget-prepare"}
	if _, err := store.PrepareModelEgress(ctx, input); !errors.Is(err, modelstore.ErrBudgetExceeded) {
		t.Fatalf("second model reservation error=%v", err)
	}
	var calls int
	if err := store.db.QueryRow(`SELECT model_calls FROM task_budget_counters WHERE task_id = ?`, taskID).Scan(&calls); err != nil || calls != 1 {
		t.Fatalf("model calls=%d error=%v", calls, err)
	}
}

func TestModelEgressFinishRejectsMismatchedIdempotentReplay(t *testing.T) {
	store, receipt, now := modelEgressFixture(t)
	defer store.Close()
	input := modelstore.FinishInput{VaultID: receipt.VaultID, ReceiptID: receipt.ID, ExpectedStatus: planning.EgressPrepared, NextStatus: planning.EgressFailed, SafeErrorCode: "MODEL_PROVIDER_UNAVAILABLE", OccurredAt: now, IdempotencyKey: "finish"}
	if _, err := store.FinishModelEgress(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	input.SafeErrorCode = "MODEL_PROVIDER_TIMEOUT"
	if _, err := store.FinishModelEgress(context.Background(), input); !errors.Is(err, modelstore.ErrIdempotencyConflict) {
		t.Fatalf("error=%v", err)
	}
}

func modelEgressFixture(t *testing.T) (*Store, planning.EgressReceipt, time.Time) {
	t.Helper()
	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	ctx := context.Background()
	now := time.Date(2026, 7, 16, 3, 0, 0, 0, time.UTC)
	vaultID := "vault-model-fixture"
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: vaultID, RetentionDays: 30, OccurredAt: now.Add(-time.Minute), IdempotencyKey: "vault"}); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateTaskContract(ctx, taskstore.CreateInput{VaultID: vaultID, WorkspaceRoot: filepath.Join(t.TempDir(), "repo"), BaselineCommit: strings.Repeat("a", 40), Goal: "plan", AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"pass"}, VerificationCommands: []task.VerificationCommand{{RuleID: "go-test", Arguments: []string{"test", "./..."}}}, Risk: task.RiskLow, OccurredAt: now.Add(-time.Second), IdempotencyKey: "task"})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := store.PrepareModelEgress(ctx, modelstore.PrepareInput{VaultID: vaultID, TaskID: created.Task.ID, ContractRevision: 1, ProviderKey: "fixture", ModelKey: "fixture-plan-v1", RequestID: "request", PromptVersion: "planning.v1", ContextHash: strings.Repeat("b", 64), RequestHash: strings.Repeat("c", 64), ContextItems: 1, InputBytes: 128, ReservedInputTokens: 128, ReservedOutputTokens: 1024, OccurredAt: now, IdempotencyKey: "prepare"})
	if err != nil {
		t.Fatal(err)
	}
	return store, receipt, now.Add(time.Second)
}
