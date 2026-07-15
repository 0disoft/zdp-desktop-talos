package patchcommand

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/application/patchreview"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/patchaction"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/verification"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspace"
)

func TestDecideRequiresFreshSafeInScopeApply(t *testing.T) {
	t.Parallel()
	record, contract, review, request := gateFixture()
	if err := Decide(record, contract, review, request); err != nil {
		t.Fatal(err)
	}
	review.SecretFindings = 1
	if err := Decide(record, contract, review, request); !errors.Is(err, ErrSecretFindings) {
		t.Fatalf("secret error=%v", err)
	}
	review.SecretFindings = 0
	review.Changes[0].Path = "outside/main.go"
	if err := Decide(record, contract, review, request); !errors.Is(err, ErrScopeViolation) {
		t.Fatalf("scope error=%v", err)
	}
	review.Changes[0].Path = "internal/main.go"
	review.Status = patchreview.StatusStale
	if err := Decide(record, contract, review, request); !errors.Is(err, ErrStaleReview) {
		t.Fatalf("stale error=%v", err)
	}
}

func TestDecideAllowsDiscardWithoutFreshEvidenceButKeepsBinding(t *testing.T) {
	t.Parallel()
	record, contract, review, request := gateFixture()
	request.Kind = patchaction.KindDiscard
	review.Status = patchreview.StatusUnverified
	review.Evidence = nil
	review.SecretFindings = 2
	review.Changes[0].Path = "outside/main.go"
	if err := Decide(record, contract, review, request); err != nil {
		t.Fatal(err)
	}
	request.ExpectedPatchHash = strings.Repeat("f", 64)
	if err := Decide(record, contract, review, request); !errors.Is(err, ErrStaleReview) {
		t.Fatalf("binding error=%v", err)
	}
}

func gateFixture() (task.Record, task.ContractRevision, patchreview.Result, Request) {
	now := time.Date(2026, 7, 15, 1, 0, 0, 0, time.UTC)
	baseline, stateHash, patchHash := strings.Repeat("a", 40), strings.Repeat("b", 64), strings.Repeat("c", 64)
	record := task.Record{ID: "task-1", VaultID: "vault-1", WorkspaceRoot: `C:\repo`, BaselineCommit: baseline, Status: task.StatusContracted, CurrentRevision: 2, CreatedAt: now, UpdatedAt: now, LastEventID: "event-1"}
	contract := task.ContractRevision{TaskID: record.ID, Revision: 2, BaselineCommit: baseline, Goal: "apply", AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"done"}, Risk: task.RiskMedium, CreatedAt: now, EventID: "event-2"}
	evidence := verification.Evidence{ID: "evidence-1", VaultID: record.VaultID, TaskID: record.ID, RunID: "run-1", AttemptID: "attempt-1", ContractRevision: 2, CommandIndex: 0, BaselineCommit: baseline, WorktreeStateHash: stateHash, CapabilityHash: strings.Repeat("d", 64), ExitCode: 0, StartedAt: now, FinishedAt: now, EventID: "event-3"}
	review := patchreview.Result{TaskID: record.ID, ContractRevision: 2, BaselineCommit: baseline, Status: patchreview.StatusFresh, StateHash: stateHash, PatchHash: patchHash, Changes: []workspace.Change{{Path: "internal/main.go", Kind: workspace.ChangeTracked, WorktreeStatus: 'M'}}, Evidence: &evidence}
	request := Request{TaskID: record.ID, Kind: patchaction.KindApply, ExpectedRevision: 2, ExpectedPatchHash: patchHash, IdempotencyKey: "apply-1"}
	return record, contract, review, request
}
