package memorykernel_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/fixturemodel"
	"github.com/0disoft/zdp-desktop-talos/internal/adapters/sqliteevent"
	"github.com/0disoft/zdp-desktop-talos/internal/application/contextassembly"
	"github.com/0disoft/zdp-desktop-talos/internal/application/executionruntime"
	"github.com/0disoft/zdp-desktop-talos/internal/application/memorykernel"
	"github.com/0disoft/zdp-desktop-talos/internal/application/modelruntime"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/permission"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/verification"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorystore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
	"github.com/0disoft/zdp-desktop-talos/internal/security/envelope"
	"github.com/0disoft/zdp-desktop-talos/internal/security/redaction"
)

func TestApprovedEncryptedMemoryChangesTheNextPlanWithProvenance(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 4, 0, 0, 0, time.UTC)
	sealer, err := envelope.NewSealer("test-key", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer sealer.Destroy()
	store, err := sqliteevent.Open(filepath.Join(t.TempDir(), "memory-loop.db"), sealer)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	vaultID := "00000000-0000-7000-8000-000000000001"
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: vaultID, RetentionDays: 30, OccurredAt: now, IdempotencyKey: "vault"}); err != nil {
		t.Fatal(err)
	}
	workspaceRoot := filepath.Join(t.TempDir(), "repo")
	created, err := store.CreateTaskContract(ctx, taskstore.CreateInput{
		VaultID: vaultID, WorkspaceRoot: workspaceRoot, BaselineCommit: strings.Repeat("a", 40), Goal: "verify decision revision checks",
		AllowedPaths: []string{"internal/domain/decision/**"}, AcceptanceCriteria: []string{"focused tests pass"},
		VerificationCommands: []task.VerificationCommand{{RuleID: "go-test", Arguments: []string{"test", "./internal/domain/decision/..."}, WorkingDirectory: "."}},
		Risk:                 task.RiskLow, OccurredAt: now.Add(time.Second), IdempotencyKey: "task",
	})
	if err != nil {
		t.Fatal(err)
	}
	kernel, err := memorykernel.New(store)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := kernel.CreateCandidate(ctx, memorystore.CreateCandidateInput{
		VaultID: vaultID, Kind: memory.KindProcedure, Scope: memory.Scope{Kind: memory.ScopeWorkspace, WorkspaceRoot: workspaceRoot},
		Statement: "Run focused decision tests before the full suite.", Rationale: "The earlier task isolated revision failures faster.",
		Applicability: memory.Applicability{GoalTerms: []string{"decision"}}, EvidenceEventIDs: []string{created.Contract.EventID},
		SourceActor: "memory-extractor", Confidence: 90, Sensitivity: event.SensitivityPrivate,
		OccurredAt: now.Add(2 * time.Second), IdempotencyKey: "candidate",
	})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := kernel.ReviewCandidate(ctx, memorystore.TransitionInput{VaultID: vaultID, MemoryID: candidate.ID, ExpectedRevision: 1, NextState: memory.StateApproved, Reason: "user approved", OccurredAt: now.Add(3 * time.Second), IdempotencyKey: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	assembler, err := contextassembly.New(store)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := fixturemodel.New("fixture", "fixture-plan-v1", []int{0})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := modelruntime.New(store, provider, redaction.NewScanner(), integrationExecutor{}, modelruntime.Policy{
		ProviderKey: "fixture", ModelKey: "fixture-plan-v1", PromptVersion: modelruntime.PlanningPromptVersion,
		MaxContextItems: 8, MaxMemoryCandidates: 32, MaxMemoryItems: 4, MaxMemoryBytes: 8 << 10, MaxInputBytes: 64 << 10,
		MaxOutputBytes: 16 << 10, MaxSteps: 4, MaxToolIntents: 4, ProviderTimeout: time.Second,
	}, modelruntime.WithMemoryContext(assembler))
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Run(ctx, modelruntime.Request{TaskID: created.Task.ID, RequestID: "request-memory-loop", IdempotencyKey: "memory-loop"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Plan.Summary, approved.Statement) || len(result.MemoryContext.Items) != 1 || result.MemoryContext.Items[0].MemoryID != approved.ID || !strings.Contains(result.MemoryContext.Items[0].SourceRef, approved.ID) || result.MemoryContext.Items[0].Reason == "" {
		t.Fatalf("result=%+v approved=%+v", result, approved)
	}
}

type integrationExecutor struct{}

func (integrationExecutor) Execute(context.Context, executionruntime.Request) (executionruntime.Result, error) {
	return executionruntime.Result{Outcome: permission.OutcomeAllowTask, Evidence: &verification.Evidence{ID: "evidence"}}, nil
}
