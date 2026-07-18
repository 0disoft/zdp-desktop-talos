package contextassembly

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspacemapping"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorycontext"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorystore"
)

func TestAssemblerSelectsOnlyApplicableApprovedMemoryWithinBudget(t *testing.T) {
	now := time.Date(2026, 7, 17, 1, 0, 0, 0, time.UTC)
	sourceHash := strings.Repeat("a", 64)
	workspaceID := workspacemapping.ID("vault", sourceHash)
	store := &reader{records: []memory.Record{
		memoryRecord("one", memory.StateStable, []string{"decision"}, "Require question revisions.", workspaceID, sourceHash, now),
		memoryRecord("two", memory.StateApproved, []string{"unrelated"}, "Do not select this.", workspaceID, sourceHash, now),
	}}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Assemble(context.Background(), memorycontext.Request{VaultID: "vault", WorkspaceID: workspaceID, Goal: "Fix decision concurrency", MaxCandidates: 8, MaxItems: 2, MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if result.Considered != 2 || len(result.Items) != 1 || result.Items[0].MemoryID != "one" || result.Items[0].Reason == "" {
		t.Fatalf("result=%+v", result)
	}
}

func TestAssemblerRejectsCrossVaultRecordFromStoreAdapter(t *testing.T) {
	now := time.Date(2026, 7, 17, 1, 0, 0, 0, time.UTC)
	sourceHash := strings.Repeat("b", 64)
	workspaceID := workspacemapping.ID("vault", sourceHash)
	record := memoryRecord("foreign", memory.StateApproved, nil, "Do not trust this.", workspaceID, sourceHash, now)
	record.VaultID = "other-vault"
	service, err := New(&reader{records: []memory.Record{record}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Assemble(context.Background(), memorycontext.Request{VaultID: "vault", WorkspaceID: workspaceID, Goal: "verify", MaxCandidates: 8, MaxItems: 2, MaxBytes: 1024})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("cross-Vault adapter record error=%v", err)
	}
}

func TestAssemblerDefensivelyExcludesExpiredAdapterRecord(t *testing.T) {
	now := time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC)
	sourceHash := strings.Repeat("c", 64)
	workspaceID := workspacemapping.ID("vault", sourceHash)
	record := memoryRecord("expired", memory.StateApproved, nil, "Old rule.", workspaceID, sourceHash, now)
	record.ExpiresAt = now.Add(time.Hour)
	service, err := New(&reader{records: []memory.Record{record}})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now.Add(2 * time.Hour) }
	result, err := service.Assemble(context.Background(), memorycontext.Request{VaultID: "vault", WorkspaceID: workspaceID, Goal: "verify", MaxCandidates: 8, MaxItems: 2, MaxBytes: 1024})
	if err != nil || result.Considered != 1 || len(result.Items) != 0 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func memoryRecord(id string, state memory.State, terms []string, statement, workspaceID, sourceHash string, now time.Time) memory.Record {
	return memory.Record{ID: id, VaultID: "vault", Kind: memory.KindConstraint, State: state, Scope: memory.Scope{Kind: memory.ScopeWorkspace, WorkspaceID: workspaceID, SourceWorkspaceHash: sourceHash}, Statement: statement, Rationale: "prior evidence", Applicability: memory.Applicability{GoalTerms: terms}, EvidenceEventIDs: []string{"event"}, SourceActor: "extractor", Confidence: 80, Sensitivity: event.SensitivityPrivate, Revision: 2, CreatedAt: now, UpdatedAt: now, ReviewedAt: now, CreatedEventID: "created", LastEventID: "last"}
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
func (r *reader) ListMemories(context.Context, memorystore.ListInput) ([]memory.Record, error) {
	return append([]memory.Record(nil), r.records...), nil
}
