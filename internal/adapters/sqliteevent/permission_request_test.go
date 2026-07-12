package sqliteevent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/permission"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/executionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
)

func TestPermissionRequestRoundTripAndAtomicResolution(t *testing.T) {
	store, vaultID, intent, now := permissionRequestFixture(t)
	defer store.Close()
	ctx := context.Background()
	input := executionstore.CreatePermissionRequestInput{VaultID: vaultID, Intent: intent, OccurredAt: now, IdempotencyKey: "permission-request"}
	created, err := store.CreatePermissionRequest(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.CreatePermissionRequest(ctx, input)
	if err != nil || replayed.ID != created.ID {
		t.Fatalf("request replay=%+v error=%v", replayed, err)
	}
	open, err := store.ListOpenPermissionRequests(ctx, vaultID, intent.TaskID, 10)
	if err != nil || len(open) != 1 || open[0].Intent.Executable != intent.Executable {
		t.Fatalf("open=%+v error=%v", open, err)
	}
	resolutionInput := executionstore.ResolvePermissionRequestInput{VaultID: vaultID, RequestID: created.ID, Outcome: permission.OutcomeAllowOnce, ExpiresAt: now.Add(time.Hour), OccurredAt: now.Add(time.Second), IdempotencyKey: "permission-resolution"}
	resolved, err := store.ResolvePermissionRequest(ctx, resolutionInput)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Request.State != permission.RequestApproved || resolved.Grant.Outcome != permission.OutcomeAllowOnce || resolved.Grant.CapabilityHash != created.CapabilityHash {
		t.Fatalf("resolved=%+v", resolved)
	}
	replayedResolution, err := store.ResolvePermissionRequest(ctx, resolutionInput)
	if err != nil || replayedResolution.Grant.ID != resolved.Grant.ID {
		t.Fatalf("resolution replay=%+v error=%v", replayedResolution, err)
	}
	open, err = store.ListOpenPermissionRequests(ctx, vaultID, intent.TaskID, 10)
	if err != nil || len(open) != 0 {
		t.Fatalf("resolved request remained open: %+v error=%v", open, err)
	}
}

func TestPermissionResolutionRollsBackGrantWhenRequestUpdateFails(t *testing.T) {
	store, vaultID, intent, now := permissionRequestFixture(t)
	defer store.Close()
	ctx := context.Background()
	created, err := store.CreatePermissionRequest(ctx, executionstore.CreatePermissionRequestInput{VaultID: vaultID, Intent: intent, OccurredAt: now, IdempotencyKey: "permission-request"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_permission_resolution BEFORE UPDATE ON permission_requests BEGIN SELECT RAISE(ABORT, 'forced resolution failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err = store.ResolvePermissionRequest(ctx, executionstore.ResolvePermissionRequestInput{VaultID: vaultID, RequestID: created.ID, Outcome: permission.OutcomeAllowTask, OccurredAt: now.Add(time.Second), IdempotencyKey: "permission-resolution"})
	if err == nil {
		t.Fatal("resolution unexpectedly succeeded")
	}
	if count := tableCount(t, store, "permission_grants"); count != 0 {
		t.Fatalf("grant survived failed resolution: %d", count)
	}
}

func TestPermissionRequestRejectsIntentFromAnotherWorkspace(t *testing.T) {
	store, vaultID, intent, now := permissionRequestFixture(t)
	defer store.Close()
	intent.WorkspaceRoot = filepath.Join(t.TempDir(), "other")
	_, err := store.CreatePermissionRequest(context.Background(), executionstore.CreatePermissionRequestInput{VaultID: vaultID, Intent: intent, OccurredAt: now, IdempotencyKey: "wrong-workspace"})
	if !errors.Is(err, executionstore.ErrConflict) {
		t.Fatalf("expected workspace conflict, got %v", err)
	}
}

func permissionRequestFixture(t *testing.T) (*Store, string, permission.ProcessIntent, time.Time) {
	t.Helper()
	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	ctx := context.Background()
	now := time.Unix(1_752_316_800, 0).UTC()
	vaultID := "vault-permission-request"
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: vaultID, RetentionDays: 30, OccurredAt: now.Add(-2 * time.Second), IdempotencyKey: "vault-create"}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "Repository")
	created, err := store.CreateTaskContract(ctx, taskstore.CreateInput{VaultID: vaultID, WorkspaceRoot: root, BaselineCommit: strings.Repeat("a", 40), Goal: "review permission", AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"reviewed"}, Risk: task.RiskHigh, OccurredAt: now.Add(-time.Second), IdempotencyKey: "task-create"})
	if err != nil {
		t.Fatal(err)
	}
	intent := permission.ProcessIntent{TaskID: created.Task.ID, WorkspaceRoot: root, RuleID: "go-test", Executable: filepath.Join(t.TempDir(), "go.exe"), Arguments: []string{"test", "./..."}, Timeout: time.Minute, MaxOutputBytes: 1024}
	return store, vaultID, intent, now
}
