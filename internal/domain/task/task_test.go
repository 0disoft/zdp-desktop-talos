package task

import (
	"errors"
	"testing"
	"time"
)

func TestContractRevisionRejectsEscapingAndDuplicatePaths(t *testing.T) {
	t.Parallel()
	valid := ContractRevision{TaskID: "task-1", Revision: 1, BaselineCommit: "0123456789012345678901234567890123456789", Goal: "Add a contract", AllowedPaths: []string{"internal/task/**"}, AcceptanceCriteria: []string{"tests pass"}, Risk: RiskMedium, CreatedAt: time.Now(), EventID: "event-1"}
	for _, paths := range [][]string{{"../secret"}, {"internal/task/**", "internal/task/**"}, {"C:/outside"}} {
		candidate := valid
		candidate.AllowedPaths = paths
		if err := candidate.Validate(); !errors.Is(err, ErrInvalidRecord) {
			t.Fatalf("paths=%v error=%v", paths, err)
		}
	}
}

func TestRecordRequiresContractedRevisionAndBaseline(t *testing.T) {
	t.Parallel()
	record := Record{ID: "task-1", VaultID: "vault-1", WorkspaceRoot: `C:\repo`, BaselineCommit: "0123456789012345678901234567890123456789", Status: StatusContracted, CurrentRevision: 1, CreatedAt: time.Now(), UpdatedAt: time.Now(), LastEventID: "event-1"}
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	record.CurrentRevision = 0
	if err := record.Validate(); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("error=%v", err)
	}
}
