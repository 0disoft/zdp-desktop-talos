package verification

import (
	"strings"
	"testing"
	"time"
)

func TestEvidenceRequiresSuccessfulStateBoundProof(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	valid := Evidence{ID: "evidence-1", VaultID: "vault-1", TaskID: "task-1", RunID: "run-1", AttemptID: "attempt-1", ContractRevision: 1, CommandIndex: 0, BaselineCommit: strings.Repeat("a", 40), WorktreeStateHash: strings.Repeat("b", 64), CapabilityHash: strings.Repeat("c", 64), ExitCode: 0, StartedAt: now, FinishedAt: now.Add(time.Second), EventID: "event-1"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.WorktreeStateHash = ""
	if invalid.Validate() == nil {
		t.Fatal("evidence without a worktree state hash was accepted")
	}
	invalid = valid
	invalid.ExitCode = 1
	if invalid.Validate() == nil {
		t.Fatal("failed command was accepted as verification evidence")
	}
}
