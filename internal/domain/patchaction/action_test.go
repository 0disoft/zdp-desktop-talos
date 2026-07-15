package patchaction

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRecordValidatesLifecycleAndBindings(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	valid := Record{ID: "action-1", VaultID: "vault-1", TaskID: "task-1", Kind: KindApply, State: StatePending, ContractRevision: 1, PatchHash: strings.Repeat("a", 64), WorktreeStateHash: strings.Repeat("b", 64), EvidenceID: "evidence-1", CreatedAt: now, UpdatedAt: now, CreatedEventID: "event-1", LastEventID: "event-1"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.EvidenceID = ""
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("missing evidence error=%v", err)
	}
	invalid = valid
	invalid.State, invalid.SafeErrorCode = StateUnknown, "PATCH_OUTCOME_UNKNOWN"
	if err := invalid.Validate(); err != nil {
		t.Fatal(err)
	}
}
