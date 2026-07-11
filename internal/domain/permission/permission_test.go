package permission

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIntentHashBindsArgumentsEnvironmentAndLimits(t *testing.T) {
	intent := ProcessIntent{TaskID: "task-1", WorkspaceRoot: filepath.Join(t.TempDir(), "repo"), RuleID: "go-test", Executable: filepath.Join(t.TempDir(), "go.exe"), Arguments: []string{"test", "./..."}, EnvironmentNames: []string{"CI"}, Timeout: time.Minute, MaxOutputBytes: 1024}
	first, err := IntentHash(intent)
	if err != nil {
		t.Fatal(err)
	}
	changed := intent
	changed.Arguments = []string{"test", "./internal/..."}
	second, err := IntentHash(changed)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || len(first) != 64 {
		t.Fatalf("hashes=%s %s", first, second)
	}
}

func TestWorkspaceIntentHashIgnoresTaskButBindsWorkspaceAndExecution(t *testing.T) {
	intent := ProcessIntent{TaskID: "task-1", WorkspaceRoot: filepath.Join(t.TempDir(), "repo"), RuleID: "go-test", Executable: filepath.Join(t.TempDir(), "go.exe"), Arguments: []string{"test", "./..."}, EnvironmentNames: []string{"CI"}, Timeout: time.Minute, MaxOutputBytes: 1024}
	first, err := WorkspaceIntentHash(intent)
	if err != nil {
		t.Fatal(err)
	}
	otherTask := intent
	otherTask.TaskID = "task-2"
	second, err := WorkspaceIntentHash(otherTask)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("workspace hash changed across tasks: %s != %s", first, second)
	}
	otherWorkspace := intent
	otherWorkspace.WorkspaceRoot = filepath.Join(t.TempDir(), "other")
	third, err := WorkspaceIntentHash(otherWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if first == third {
		t.Fatal("workspace hash did not bind the workspace root")
	}
}

func TestIntentHashCanonicalizesEnvironmentSet(t *testing.T) {
	intent := ProcessIntent{TaskID: "task-1", WorkspaceRoot: filepath.Join(t.TempDir(), "repo"), RuleID: "go-test", Executable: filepath.Join(t.TempDir(), "go.exe"), Arguments: []string{"test"}, EnvironmentNames: []string{"CI", "HOME"}, Timeout: time.Minute, MaxOutputBytes: 1024}
	first, err := IntentHash(intent)
	if err != nil {
		t.Fatal(err)
	}
	intent.EnvironmentNames = []string{"HOME", "CI"}
	second, err := IntentHash(intent)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("environment order changed intent hash: %s != %s", first, second)
	}
	intent.EnvironmentNames = []string{"CI", "CI"}
	if _, err := IntentHash(intent); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("duplicate environment error=%v", err)
	}
}

func TestGrantValidationRequiresScopedIdentity(t *testing.T) {
	now := time.Now().UTC()
	valid := Grant{ID: "grant-1", Outcome: OutcomeAllowTask, State: GrantActive, CapabilityHash: strings.Repeat("a", 64), TaskID: "task-1", WorkspaceHash: strings.Repeat("b", 64), CreatedAt: now}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.TaskID = ""
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("error=%v", err)
	}
	invalid = valid
	invalid.WorkspaceHash = strings.Repeat("G", 64)
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("workspace hash error=%v", err)
	}
}
