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

func TestRecordRequiresKnownStatusRevisionAndBaseline(t *testing.T) {
	t.Parallel()
	record := Record{ID: "task-1", VaultID: "vault-1", WorkspaceRoot: `C:\repo`, BaselineCommit: "0123456789012345678901234567890123456789", Status: StatusContracted, CurrentRevision: 1, CreatedAt: time.Now(), UpdatedAt: time.Now(), LastEventID: "event-1"}
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, status := range []Status{StatusCompleted, StatusDiscarded} {
		record.Status = status
		if err := record.Validate(); err != nil {
			t.Fatalf("status=%s error=%v", status, err)
		}
	}
	record.Status = "unknown"
	if err := record.Validate(); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("unknown status error=%v", err)
	}
	record.Status = StatusContracted
	record.CurrentRevision = 0
	if err := record.Validate(); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("error=%v", err)
	}
}

func TestContractRevisionMatchesAllowedPaths(t *testing.T) {
	t.Parallel()
	contract := ContractRevision{AllowedPaths: []string{"internal/task/**", "README.md", "frontend/*.json"}}
	for _, candidate := range []string{"internal/task/task.go", "internal/task", "README.md", "frontend/package.json"} {
		if !contract.AllowsPath(candidate) {
			t.Fatalf("expected allowed: %s", candidate)
		}
	}
	for _, candidate := range []string{"internal/other.go", "frontend/src/main.ts", "../secret", "/absolute"} {
		if contract.AllowsPath(candidate) {
			t.Fatalf("unexpected allowed: %s", candidate)
		}
	}
}

func TestContractRevisionValidatesStructuredVerificationCommands(t *testing.T) {
	t.Parallel()
	valid := ContractRevision{TaskID: "task-1", Revision: 1, BaselineCommit: "0123456789012345678901234567890123456789", Goal: "Add verification", AllowedPaths: []string{"internal/task/**"}, AcceptanceCriteria: []string{"tests pass"}, VerificationCommands: []VerificationCommand{{RuleID: "go-test", Arguments: []string{"test", "./internal/domain/task/..."}, WorkingDirectory: "."}}, Risk: RiskMedium, CreatedAt: time.Now(), EventID: "event-1"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := []VerificationCommand{
		{RuleID: "Go Test", Arguments: []string{"test"}, WorkingDirectory: "."},
		{RuleID: "go-test", Arguments: []string{""}, WorkingDirectory: "."},
		{RuleID: "go-test", Arguments: []string{"test"}, WorkingDirectory: "../outside"},
		{RuleID: "go-test", Arguments: []string{"test"}, WorkingDirectory: "internal/*"},
	}
	for _, command := range cases {
		candidate := valid
		candidate.VerificationCommands = []VerificationCommand{command}
		if err := candidate.Validate(); !errors.Is(err, ErrInvalidRecord) {
			t.Fatalf("command=%+v error=%v", command, err)
		}
	}
	candidate := valid
	candidate.VerificationCommands = append(candidate.VerificationCommands, candidate.VerificationCommands[0])
	if err := candidate.Validate(); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("duplicate error=%v", err)
	}
}

func TestVerificationCommandNormalizesRepositoryRootWithoutMutatingArguments(t *testing.T) {
	t.Parallel()
	command := VerificationCommand{RuleID: "go-test", Arguments: []string{"test", "./..."}}
	normalized, err := command.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if normalized.WorkingDirectory != "." || normalized.RuleID != command.RuleID || len(normalized.Arguments) != 2 {
		t.Fatalf("normalized=%+v", normalized)
	}
	normalized.Arguments[0] = "changed"
	if command.Arguments[0] != "test" {
		t.Fatal("normalization aliased argument storage")
	}
}
