package permissionbroker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/permission"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
)

func TestBrokerRequiresReviewThenAppliesExactScopedGrant(t *testing.T) {
	record, contract, intent, rule := brokerFixture(t)
	broker, err := New([]ProcessRule{rule})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	broker.now = func() time.Time { return now }
	if evaluation := broker.Evaluate(record, contract, intent, nil); evaluation.Outcome != permission.OutcomeRequireReview || evaluation.Capability != nil {
		t.Fatalf("review=%+v", evaluation)
	}
	hash, _ := permission.IntentHash(intent)
	workspaceHash, _ := permission.WorkspaceHash(record.WorkspaceRoot)
	grant := permission.Grant{ID: "grant-once", Outcome: permission.OutcomeAllowOnce, State: permission.GrantActive, CapabilityHash: hash, TaskID: record.ID, WorkspaceHash: workspaceHash, CreatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute)}
	evaluation := broker.Evaluate(record, contract, intent, []permission.Grant{grant})
	if evaluation.Outcome != permission.OutcomeAllowOnce || !evaluation.ConsumesGrant || evaluation.MatchedGrantID != grant.ID || evaluation.Capability == nil {
		t.Fatalf("allow=%+v", evaluation)
	}
	if evaluation.Capability.MaxArguments != len(intent.Arguments) || strings.Join(evaluation.Capability.ArgumentPrefix, "|") != strings.Join(intent.Arguments, "|") || evaluation.Capability.IntentHash != hash {
		t.Fatalf("capability=%+v", evaluation.Capability)
	}
	changed := intent
	changed.Arguments = append(append([]string(nil), intent.Arguments...), "-run", "Danger")
	if evaluation = broker.Evaluate(record, contract, changed, []permission.Grant{grant}); evaluation.Outcome != permission.OutcomeDeny || evaluation.ReasonCode != "PROCESS_RULE_MISMATCH" {
		t.Fatalf("changed=%+v", evaluation)
	}
}

func TestBrokerContractDenyOverridesGrantsAndExplicitDenyWins(t *testing.T) {
	record, contract, intent, rule := brokerFixture(t)
	broker, _ := New([]ProcessRule{rule})
	now := time.Now().UTC()
	broker.now = func() time.Time { return now }
	hash, _ := permission.IntentHash(intent)
	workspaceIntentHash, _ := permission.WorkspaceIntentHash(intent)
	workspaceHash, _ := permission.WorkspaceHash(record.WorkspaceRoot)
	allow := permission.Grant{ID: "allow", Outcome: permission.OutcomeAllowTask, State: permission.GrantActive, CapabilityHash: hash, TaskID: record.ID, WorkspaceHash: workspaceHash, CreatedAt: now}
	deny := permission.Grant{ID: "deny", Outcome: permission.OutcomeDeny, State: permission.GrantActive, CapabilityHash: workspaceIntentHash, WorkspaceHash: workspaceHash, CreatedAt: now}
	if result := broker.Evaluate(record, contract, intent, []permission.Grant{allow, deny}); result.Outcome != permission.OutcomeDeny || result.ReasonCode != "EXPLICIT_GRANT_DENY" {
		t.Fatalf("deny=%+v", result)
	}
	contract.ForbiddenActions = []string{"process.exec"}
	if result := broker.Evaluate(record, contract, intent, []permission.Grant{allow}); result.Outcome != permission.OutcomeDeny || result.ReasonCode != "TASK_CONTRACT_FORBIDS_PROCESS" {
		t.Fatalf("contract=%+v", result)
	}
}

func TestBrokerIgnoresExpiredConsumedAndWrongScopeGrants(t *testing.T) {
	record, contract, intent, rule := brokerFixture(t)
	broker, _ := New([]ProcessRule{rule})
	now := time.Now().UTC()
	broker.now = func() time.Time { return now }
	hash, _ := permission.IntentHash(intent)
	workspaceHash, _ := permission.WorkspaceHash(record.WorkspaceRoot)
	base := permission.Grant{ID: "grant", Outcome: permission.OutcomeAllowTask, State: permission.GrantActive, CapabilityHash: hash, TaskID: record.ID, WorkspaceHash: workspaceHash, CreatedAt: now.Add(-time.Hour)}
	cases := []permission.Grant{base, base, base}
	cases[0].ExpiresAt = now.Add(-time.Second)
	cases[1].State = permission.GrantConsumed
	cases[2].TaskID = "other-task"
	for index, grant := range cases {
		grant.ID += string(rune('a' + index))
		if result := broker.Evaluate(record, contract, intent, []permission.Grant{grant}); result.Outcome != permission.OutcomeRequireReview {
			t.Fatalf("case %d=%+v", index, result)
		}
	}
}

func TestBrokerAppliesWorkspaceGrantAcrossTasksWithoutBroadeningCapability(t *testing.T) {
	record, contract, intent, rule := brokerFixture(t)
	broker, err := New([]ProcessRule{rule})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	broker.now = func() time.Time { return now }
	workspaceIntentHash, err := permission.WorkspaceIntentHash(intent)
	if err != nil {
		t.Fatal(err)
	}
	workspaceHash, err := permission.WorkspaceHash(record.WorkspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	grant := permission.Grant{ID: "grant-workspace", Outcome: permission.OutcomeAllowWorkspace, State: permission.GrantActive, CapabilityHash: workspaceIntentHash, WorkspaceHash: workspaceHash, CreatedAt: now}

	record.ID = "task-2"
	contract.TaskID = record.ID
	intent.TaskID = record.ID
	evaluation := broker.Evaluate(record, contract, intent, []permission.Grant{grant})
	if evaluation.Outcome != permission.OutcomeAllowWorkspace || evaluation.Capability == nil || evaluation.ConsumesGrant {
		t.Fatalf("workspace evaluation=%+v", evaluation)
	}
	taskHash, err := permission.IntentHash(intent)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Capability.IntentHash != taskHash || evaluation.Capability.MaxArguments != len(intent.Arguments) {
		t.Fatalf("capability=%+v", evaluation.Capability)
	}
}

func TestBrokerUsesGrantIDToBreakEqualScopeTies(t *testing.T) {
	record, contract, intent, rule := brokerFixture(t)
	broker, err := New([]ProcessRule{rule})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	broker.now = func() time.Time { return now }
	hash, _ := permission.IntentHash(intent)
	workspaceHash, _ := permission.WorkspaceHash(record.WorkspaceRoot)
	grantZ := permission.Grant{ID: "grant-z", Outcome: permission.OutcomeAllowTask, State: permission.GrantActive, CapabilityHash: hash, TaskID: record.ID, WorkspaceHash: workspaceHash, CreatedAt: now}
	grantA := grantZ
	grantA.ID = "grant-a"
	evaluation := broker.Evaluate(record, contract, intent, []permission.Grant{grantZ, grantA})
	if evaluation.MatchedGrantID != grantA.ID {
		t.Fatalf("matched grant=%q", evaluation.MatchedGrantID)
	}
}

func TestBrokerRejectsShellPolicy(t *testing.T) {
	root := os.Getenv("SystemRoot")
	if root == "" {
		t.Skip("Windows shell path unavailable")
	}
	_, err := New([]ProcessRule{{ID: "shell", Executable: filepath.Join(root, "System32", "cmd.exe"), ArgumentPrefix: []string{"/c"}, MaxTimeout: time.Second, MaxOutputBytes: 1024, Default: permission.OutcomeRequireReview}})
	if err == nil {
		t.Fatal("shell policy accepted")
	}
}

func brokerFixture(t *testing.T) (task.Record, task.ContractRevision, permission.ProcessIntent, ProcessRule) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, _ = filepath.EvalSymlinks(executable)
	root := t.TempDir()
	now := time.Now().UTC()
	baseline := strings.Repeat("a", 40)
	record := task.Record{ID: "task-1", VaultID: "vault-1", WorkspaceRoot: root, BaselineCommit: baseline, Status: task.StatusContracted, CurrentRevision: 1, CreatedAt: now, UpdatedAt: now, LastEventID: "event-1"}
	contract := task.ContractRevision{TaskID: record.ID, Revision: 1, BaselineCommit: baseline, Goal: "test", AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"pass"}, Risk: task.RiskLow, CreatedAt: now, EventID: "event-2"}
	intent := permission.ProcessIntent{TaskID: record.ID, WorkspaceRoot: root, RuleID: "go-test", Executable: executable, Arguments: []string{"-test.run=TestIPCExecHelper"}, EnvironmentNames: []string{"CI"}, Timeout: time.Minute, MaxOutputBytes: 1024}
	rule := ProcessRule{ID: intent.RuleID, Executable: executable, ArgumentPrefix: []string{"-test.run=TestIPCExecHelper"}, MaxArguments: 2, EnvironmentNames: []string{"CI"}, MaxTimeout: 2 * time.Minute, MaxOutputBytes: 2048, Default: permission.OutcomeRequireReview}
	return record, contract, intent, rule
}
