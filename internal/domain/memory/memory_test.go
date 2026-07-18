package memory

import (
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspacemapping"
)

func TestRecordRequiresNormalizedProvenanceAndReviewLifecycle(t *testing.T) {
	now := time.Date(2026, 7, 17, 1, 0, 0, 0, time.UTC)
	record := Record{
		ID: "memory-1", VaultID: "vault-1", Kind: KindProcedure, State: StateCandidate,
		Scope: Scope{Kind: ScopeWorkspace, WorkspaceID: workspacemapping.ID("vault", strings.Repeat("a", 64)), SourceWorkspaceHash: strings.Repeat("a", 64)}, Statement: "Run focused tests first.",
		Rationale: "The prior task failed after a broad test run.", Applicability: Applicability{GoalTerms: []string{"tests"}},
		EvidenceEventIDs: []string{"event-1"}, SourceActor: "memory-extractor", Confidence: 80,
		Sensitivity: event.SensitivityPrivate, Revision: 1, CreatedAt: now, UpdatedAt: now,
		CreatedEventID: "event-created", LastEventID: "event-created",
	}
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	record.Applicability.GoalTerms = []string{" Tests "}
	if err := record.Validate(); err == nil {
		t.Fatal("non-normalized applicability term was accepted")
	}
	record.Applicability.GoalTerms = []string{"tests"}
	record.Sensitivity = event.SensitivitySecret
	if err := record.Validate(); err == nil {
		t.Fatal("secret memory was accepted")
	}
}

func TestStateTransitionsKeepTerminalStatesClosed(t *testing.T) {
	allowed := [][2]State{{StateCandidate, StateApproved}, {StateCandidate, StateRejected}, {StateApproved, StateStable}, {StateStable, StateStale}, {StateStale, StateApproved}}
	for _, pair := range allowed {
		if !CanTransition(pair[0], pair[1]) {
			t.Fatalf("expected transition %s -> %s", pair[0], pair[1])
		}
	}
	for _, state := range []State{StateRejected, StateQuarantined, StateSuperseded, StateDeprecated} {
		if CanTransition(state, StateApproved) {
			t.Fatalf("terminal state %s reopened", state)
		}
	}
}

func TestExpiryAndSupersessionRemainExplicit(t *testing.T) {
	now := time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC)
	record := Record{ID: "memory-old", VaultID: "vault", Kind: KindDecision, State: StateApproved, Scope: Scope{Kind: ScopeVault}, Statement: "Use revision checks.", Rationale: "User decision.", EvidenceEventIDs: []string{"event"}, SourceActor: "user", Confidence: 100, Sensitivity: event.SensitivityPrivate, Revision: 2, CreatedAt: now, UpdatedAt: now, ReviewedAt: now, ExpiresAt: now.Add(time.Hour), CreatedEventID: "created", LastEventID: "last"}
	if record.Validate() != nil || record.IsExpired(now.Add(59*time.Minute)) || !record.IsExpired(now.Add(time.Hour)) {
		t.Fatalf("record=%+v", record)
	}
	record.State = StateSuperseded
	if record.Validate() == nil {
		t.Fatal("superseded memory without replacement was accepted")
	}
	record.SupersededBy = "memory-new"
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
}
