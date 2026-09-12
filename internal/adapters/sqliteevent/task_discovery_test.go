package sqliteevent

import (
	"context"
	"fmt"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/patchaction"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/patchstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTaskDiscoveryPagesAndFiltersBeforeLimit(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	store := openTestStore(t, filepath.Join(t.TempDir(), "tasks.db"))
	defer store.Close()
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: "vault-pages", RetentionDays: 30, OccurredAt: now, IdempotencyKey: "vault"}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	baseline := strings.Repeat("a", 40)
	other := strings.Repeat("b", 40)
	for i := 0; i < 53; i++ {
		base := baseline
		if i == 52 {
			base = other
		}
		created, err := store.CreateTaskContract(ctx, taskstore.CreateInput{VaultID: "vault-pages", WorkspaceRoot: root, BaselineCommit: base, Goal: fmt.Sprintf("task %d", i), AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"pass"}, VerificationCommands: []task.VerificationCommand{{RuleID: "go-test", Arguments: []string{"test", "./..."}, WorkingDirectory: "."}}, Risk: task.RiskLow, OccurredAt: now.Add(time.Duration(i) * time.Second), IdempotencyKey: fmt.Sprint(i)})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			prepared, err := store.PreparePatchAction(ctx, patchstore.PrepareInput{VaultID: "vault-pages", TaskID: created.Task.ID, Kind: patchaction.KindDiscard, ContractRevision: 1, PatchHash: strings.Repeat("d", 64), WorktreeStateHash: strings.Repeat("e", 64), OccurredAt: now.Add(time.Minute), IdempotencyKey: "discard"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.FinishPatchAction(ctx, patchstore.FinishInput{VaultID: "vault-pages", ActionID: prepared.Action.ID, ExpectedState: patchaction.StatePending, NextState: patchaction.StateSucceeded, OccurredAt: now.Add(2 * time.Minute), IdempotencyKey: "discard-finish"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	input := taskstore.ListInput{VaultID: "vault-pages", WorkspaceRoot: root, BaselineCommit: baseline, Limit: 50, Paged: true}
	first, err := store.ListTaskContracts(ctx, input)
	if err != nil || len(first) != 50 {
		t.Fatalf("first=%d err=%v", len(first), err)
	}
	last := first[len(first)-1].Task
	input.BeforeCreatedAt = last.CreatedAt.Format(time.RFC3339Nano)
	input.BeforeID = last.ID
	second, err := store.ListTaskContracts(ctx, input)
	if err != nil || len(second) != 2 {
		t.Fatalf("second=%d err=%v", len(second), err)
	}
	seen := map[string]bool{}
	for _, v := range first {
		seen[v.Task.ID] = true
	}
	for _, v := range second {
		if seen[v.Task.ID] {
			t.Fatal("duplicate page item")
		}
	}
	input.BeforeID = ""
	input.BeforeCreatedAt = ""
	input.Status = "discarded"
	filtered, err := store.ListTaskContracts(ctx, input)
	if err != nil || len(filtered) != 1 || filtered[0].Contract.Goal != "task 0" {
		t.Fatalf("filtered=%+v err=%v", filtered, err)
	}
	input.Status = ""
	input.AllBaselines = true
	all, err := store.ListTaskContracts(ctx, input)
	if err != nil || len(all) != 50 || all[0].Task.BaselineCommit != other {
		t.Fatalf("all=%+v err=%v", all, err)
	}
	input.BeforeID = "bad"
	input.BeforeCreatedAt = "bad"
	if _, err := store.ListTaskContracts(ctx, input); err == nil {
		t.Fatal("invalid cursor accepted")
	}
}
