package sqliteevent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/eventstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorystore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
)

func TestMemoryCandidateGatePersistsEncryptedProvenanceAndFiltersContext(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 2, 0, 0, 0, time.UTC)
	store := openTestStore(t, filepath.Join(t.TempDir(), "memory.db"))
	store.now = func() time.Time { return now }
	defer store.Close()
	vaultID := "00000000-0000-7000-8000-000000000001"
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: vaultID, RetentionDays: 30, OccurredAt: now, IdempotencyKey: "vault"}); err != nil {
		t.Fatal(err)
	}
	evidence, err := store.Append(ctx, eventstore.AppendInput{VaultID: vaultID, Type: "task.outcome.observed", SchemaVersion: 1, Sensitivity: event.SensitivityPrivate, Payload: []byte(`{"task_id":"task-1","status":"completed"}`), OccurredAt: now.Add(time.Second), IdempotencyKey: "evidence"})
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := filepath.Join(t.TempDir(), "repo")
	input := memorystore.CreateCandidateInput{
		VaultID: vaultID, Kind: memory.KindProcedure, Scope: memory.Scope{Kind: memory.ScopeWorkspace, WorkspaceRoot: workspaceRoot},
		Statement: "Run focused tests before the full suite.", Rationale: "A prior broad run hid the first failure.",
		Applicability: memory.Applicability{GoalTerms: []string{"tests"}}, EvidenceEventIDs: []string{evidence.ID},
		SourceActor: "memory-extractor", Confidence: 85, Sensitivity: event.SensitivityPrivate,
		OccurredAt: now.Add(2 * time.Second), IdempotencyKey: "candidate",
	}
	candidate, err := store.CreateMemoryCandidate(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.CreateMemoryCandidate(ctx, input)
	if err != nil || replayed.ID != candidate.ID {
		t.Fatalf("replayed=%+v error=%v", replayed, err)
	}
	if candidate.State != memory.StateCandidate || candidate.Revision != 1 || candidate.CreatedEventID != candidate.LastEventID {
		t.Fatalf("candidate=%+v", candidate)
	}
	var plaintextCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM events WHERE CAST(payload_envelope AS TEXT) LIKE '%Run focused tests%'`).Scan(&plaintextCount); err != nil || plaintextCount != 0 {
		t.Fatalf("memory statement leaked: count=%d error=%v", plaintextCount, err)
	}
	approved, err := store.TransitionMemory(ctx, memorystore.TransitionInput{VaultID: vaultID, MemoryID: candidate.ID, ExpectedRevision: 1, NextState: memory.StateApproved, Reason: "user approved", OccurredAt: now.Add(3 * time.Second), IdempotencyKey: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	if approved.State != memory.StateApproved || approved.Revision != 2 || approved.ReviewedAt.IsZero() || approved.CreatedEventID != candidate.CreatedEventID {
		t.Fatalf("approved=%+v", approved)
	}
	active, err := store.ListActiveMemories(ctx, memorystore.ListActiveInput{VaultID: vaultID, WorkspaceRoot: filepath.Join(workspaceRoot, "."), Limit: 10})
	if err != nil || len(active) != 1 || active[0].ID != candidate.ID {
		t.Fatalf("active=%+v error=%v", active, err)
	}
	otherWorkspace, err := store.ListActiveMemories(ctx, memorystore.ListActiveInput{VaultID: vaultID, WorkspaceRoot: filepath.Join(t.TempDir(), "other"), Limit: 10})
	if err != nil || len(otherWorkspace) != 0 {
		t.Fatalf("other workspace=%+v error=%v", otherWorkspace, err)
	}
	if _, err := store.TransitionMemory(ctx, memorystore.TransitionInput{VaultID: vaultID, MemoryID: candidate.ID, ExpectedRevision: 1, NextState: memory.StateStable, Reason: "stale", IdempotencyKey: "stale"}); !errors.Is(err, memorystore.ErrRevisionConflict) {
		t.Fatalf("stale transition error=%v", err)
	}
}

func TestMemoryCandidateRejectsMissingOrCrossVaultEvidence(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 3, 0, 0, 0, time.UTC)
	store := openTestStore(t, filepath.Join(t.TempDir(), "memory-evidence.db"))
	defer store.Close()
	for index, vaultID := range []string{"00000000-0000-7000-8000-000000000001", "00000000-0000-7000-8000-000000000002"} {
		if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: vaultID, RetentionDays: 30, OccurredAt: now.Add(time.Duration(index) * time.Second), IdempotencyKey: "vault-" + vaultID}); err != nil {
			t.Fatal(err)
		}
	}
	evidence, err := store.Append(ctx, eventstore.AppendInput{VaultID: "00000000-0000-7000-8000-000000000002", Type: "foreign.evidence", SchemaVersion: 1, Sensitivity: event.SensitivityPrivate, Payload: []byte(`{"safe":true}`), OccurredAt: now.Add(2 * time.Second), IdempotencyKey: "foreign"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateMemoryCandidate(ctx, memorystore.CreateCandidateInput{VaultID: "00000000-0000-7000-8000-000000000001", Kind: memory.KindConstraint, Scope: memory.Scope{Kind: memory.ScopeVault}, Statement: "Never bypass revision checks.", Rationale: "Evidence must stay Vault-local.", Applicability: memory.Applicability{}, EvidenceEventIDs: []string{evidence.ID}, SourceActor: "extractor", Confidence: 90, Sensitivity: event.SensitivityPrivate, OccurredAt: now.Add(3 * time.Second), IdempotencyKey: strings.Repeat("x", 12)})
	if !errors.Is(err, memorystore.ErrEvidenceNotFound) {
		t.Fatalf("cross-Vault evidence error=%v", err)
	}
}

func TestMemoryExpiryAndSupersessionAreAtomicAndContextSafe(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 4, 0, 0, 0, time.UTC)
	store := openTestStore(t, filepath.Join(t.TempDir(), "memory-lifecycle.db"))
	defer store.Close()
	vaultID := "00000000-0000-7000-8000-000000000001"
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: vaultID, RetentionDays: 30, OccurredAt: now, IdempotencyKey: "vault"}); err != nil {
		t.Fatal(err)
	}
	evidence, err := store.Append(ctx, eventstore.AppendInput{VaultID: vaultID, Type: "decision.answer.recorded", SchemaVersion: 1, Sensitivity: event.SensitivityPrivate, Payload: []byte(`{"safe":true}`), OccurredAt: now.Add(time.Second), IdempotencyKey: "evidence"})
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := filepath.Join(t.TempDir(), "repo")
	create := func(key, statement string, at time.Time) memory.Record {
		record, err := store.CreateMemoryCandidate(ctx, memorystore.CreateCandidateInput{VaultID: vaultID, Kind: memory.KindDecision, Scope: memory.Scope{Kind: memory.ScopeWorkspace, WorkspaceRoot: workspaceRoot}, Statement: statement, Rationale: "user-confirmed decision", EvidenceEventIDs: []string{evidence.ID}, SourceActor: "user", Confidence: 100, Sensitivity: event.SensitivityPrivate, OccurredAt: at, IdempotencyKey: key})
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	old := create("old", "Use the old rule.", now.Add(2*time.Second))
	replacement := create("replacement", "Use the replacement rule.", now.Add(3*time.Second))
	old, err = store.TransitionMemory(ctx, memorystore.TransitionInput{VaultID: vaultID, MemoryID: old.ID, ExpectedRevision: 1, NextState: memory.StateApproved, Reason: "approved", ExpiresAt: now.Add(10*time.Minute + 900*time.Millisecond), OccurredAt: now.Add(4 * time.Second), IdempotencyKey: "approve-old"})
	if err != nil {
		t.Fatal(err)
	}
	replacement, err = store.TransitionMemory(ctx, memorystore.TransitionInput{VaultID: vaultID, MemoryID: replacement.ID, ExpectedRevision: 1, NextState: memory.StateApproved, Reason: "approved", OccurredAt: now.Add(5 * time.Second), IdempotencyKey: "approve-replacement"})
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.ListActiveMemories(ctx, memorystore.ListActiveInput{VaultID: vaultID, WorkspaceRoot: workspaceRoot, Limit: 10, At: now.Add(9 * time.Minute)})
	if err != nil || len(active) != 2 {
		t.Fatalf("active=%+v error=%v", active, err)
	}
	active, err = store.ListActiveMemories(ctx, memorystore.ListActiveInput{VaultID: vaultID, WorkspaceRoot: workspaceRoot, Limit: 10, At: now.Add(10*time.Minute + 950*time.Millisecond)})
	if err != nil || len(active) != 1 || active[0].ID != replacement.ID {
		t.Fatalf("subsecond post-expiry active=%+v error=%v", active, err)
	}
	active, err = store.ListActiveMemories(ctx, memorystore.ListActiveInput{VaultID: vaultID, WorkspaceRoot: workspaceRoot, Limit: 10, At: now.Add(11 * time.Minute)})
	if err != nil || len(active) != 1 || active[0].ID != replacement.ID {
		t.Fatalf("post-expiry active=%+v error=%v", active, err)
	}
	old, err = store.TransitionMemory(ctx, memorystore.TransitionInput{VaultID: vaultID, MemoryID: old.ID, ExpectedRevision: old.Revision, NextState: memory.StateSuperseded, SupersededBy: replacement.ID, Reason: "replacement approved", OccurredAt: now.Add(12 * time.Minute), IdempotencyKey: "supersede-old"})
	if err != nil || old.State != memory.StateSuperseded || old.SupersededBy != replacement.ID || old.Revision != 3 {
		t.Fatalf("old=%+v error=%v", old, err)
	}
	all, err := store.ListMemories(ctx, memorystore.ListInput{VaultID: vaultID, Limit: 10})
	if err != nil || len(all) != 2 {
		t.Fatalf("all=%+v error=%v", all, err)
	}
}
