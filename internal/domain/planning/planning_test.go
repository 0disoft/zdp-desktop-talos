package planning

import (
	"strings"
	"testing"
	"time"
)

func TestPlanRejectsDuplicateVerificationCommand(t *testing.T) {
	plan := Plan{SchemaVersion: SchemaVersion, Summary: "bounded", Steps: []Step{
		{ID: "step-a", Purpose: "first", Tool: ToolIntent{ID: "tool-a", Kind: ToolVerificationCommand, CommandIndex: 0}},
		{ID: "step-b", Purpose: "again", Tool: ToolIntent{ID: "tool-b", Kind: ToolVerificationCommand, CommandIndex: 0}},
	}}
	if plan.Validate() == nil {
		t.Fatal("duplicate command index was accepted")
	}
}

func TestEgressReceiptContainsOnlyBoundedMetadata(t *testing.T) {
	now := time.Date(2026, 7, 16, 1, 0, 0, 0, time.UTC)
	receipt := EgressReceipt{
		ID: "receipt", VaultID: "vault", TaskID: "task", ContractRevision: 1,
		ProviderKey: "fixture", ModelKey: "fixture-plan-v1", RequestID: "request", PromptVersion: "planning.v1",
		ContextHash: strings.Repeat("a", 64), RequestHash: strings.Repeat("b", 64), ResponseHash: strings.Repeat("c", 64),
		ProviderCallID: "fixture-call",
		ContextItems:   1, InputBytes: 10, OutputBytes: 20, Status: EgressCompleted, CreatedAt: now, UpdatedAt: now,
		CreatedEventID: "created-event", LastEventID: "finished-event",
	}
	if err := receipt.Validate(); err != nil {
		t.Fatal(err)
	}
	receipt.SafeErrorCode = "RAW_SECRET_VALUE"
	if receipt.Validate() == nil {
		t.Fatal("completed receipt accepted an error field")
	}
}
