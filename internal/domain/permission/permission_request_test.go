package permission

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPermissionRequestBindsExactIntentAndWorkspace(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	intent := ProcessIntent{TaskID: "task-1", WorkspaceRoot: root, RuleID: "go-test", Executable: filepath.Join(t.TempDir(), "go.exe"), Arguments: []string{"test", "./..."}, Timeout: time.Minute, MaxOutputBytes: 1024}
	workspaceHash, _ := WorkspaceHash(root)
	capabilityHash, _ := IntentHash(intent)
	now := time.Now().UTC()
	request := Request{ID: "request-1", VaultID: "vault-1", TaskID: intent.TaskID, WorkspaceHash: workspaceHash, CapabilityHash: capabilityHash, Intent: intent, State: RequestOpen, CreatedAt: now, UpdatedAt: now, LastEventID: "event-1"}
	if err := request.Validate(); err != nil {
		t.Fatalf("validate request: %v", err)
	}
	request.CapabilityHash = strings.Repeat("a", 64)
	if err := request.Validate(); err == nil {
		t.Fatal("request accepted a mismatched intent hash")
	}
}
