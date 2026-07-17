package contextassembly

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorycontext"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorystore"
)

func TestAssemblerSelectsOnlyApplicableApprovedMemoryWithinBudget(t *testing.T) {
	now := time.Date(2026, 7, 17, 1, 0, 0, 0, time.UTC)
	workspaceRoot := filepath.Join(t.TempDir(), "repo")
	store := &reader{records: []memory.Record{
		memoryRecord("one", memory.StateStable, []string{"decision"}, "Require question revisions.", workspaceRoot, now),
		memoryRecord("two", memory.StateApproved, []string{"unrelated"}, "Do not select this.", workspaceRoot, now),
	}}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Assemble(context.Background(), memorycontext.Request{VaultID: "vault", WorkspaceRoot: workspaceRoot, Goal: "Fix decision concurrency", MaxCandidates: 8, MaxItems: 2, MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if result.Considered != 2 || len(result.Items) != 1 || result.Items[0].MemoryID != "one" || result.Items[0].Reason == "" {
		t.Fatalf("result=%+v", result)
	}
}

func TestAssemblerRejectsCrossVaultRecordFromStoreAdapter(t *testing.T) {
	now := time.Date(2026, 7, 17, 1, 0, 0, 0, time.UTC)
	workspaceRoot := filepath.Join(t.TempDir(), "repo")
	record := memoryRecord("foreign", memory.StateApproved, nil, "Do not trust this.", workspaceRoot, now)
	record.VaultID = "other-vault"
	service, err := New(&reader{records: []memory.Record{record}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Assemble(context.Background(), memorycontext.Request{VaultID: "vault", WorkspaceRoot: workspaceRoot, Goal: "verify", MaxCandidates: 8, MaxItems: 2, MaxBytes: 1024})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("cross-Vault adapter record error=%v", err)
	}
}

func memoryRecord(id string, state memory.State, terms []string, statement, workspaceRoot string, now time.Time) memory.Record {
	return memory.Record{ID: id, VaultID: "vault", Kind: memory.KindConstraint, State: state, Scope: memory.Scope{Kind: memory.ScopeWorkspace, WorkspaceRoot: workspaceRoot}, Statement: statement, Rationale: "prior evidence", Applicability: memory.Applicability{GoalTerms: terms}, EvidenceEventIDs: []string{"event"}, SourceActor: "extractor", Confidence: 80, Sensitivity: event.SensitivityPrivate, Revision: 2, CreatedAt: now, UpdatedAt: now, ReviewedAt: now, CreatedEventID: "created", LastEventID: "last"}
}

type reader struct{ records []memory.Record }

func (*reader) GetMemory(context.Context, string, string) (memory.Record, error) {
	return memory.Record{}, memorystore.ErrNotFound
}
func (*reader) ListMemoryCandidates(context.Context, string, int) ([]memory.Record, error) {
	return nil, nil
}
func (r *reader) ListActiveMemories(context.Context, memorystore.ListActiveInput) ([]memory.Record, error) {
	return append([]memory.Record(nil), r.records...), nil
}
