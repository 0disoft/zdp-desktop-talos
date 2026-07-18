package sqliteevent

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspacemapping"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/workspacestore"
)

func TestWorkspaceMappingIsDeviceLocalRevisionedAndEncrypted(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "mapping.db")
	store := openTestStore(t, databasePath)
	now := time.Date(2026, 7, 18, 16, 0, 0, 0, time.UTC)
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: "vault-map", RetentionDays: 30, OccurredAt: now, IdempotencyKey: "vault-map"}); err != nil {
		t.Fatal(err)
	}
	sourceRoot := `C:\talos-private-source-marker`
	baseline := strings.Repeat("a", 40)
	created, err := store.CreateTaskContract(ctx, taskstore.CreateInput{VaultID: "vault-map", WorkspaceRoot: sourceRoot, BaselineCommit: baseline, Goal: "map the imported workspace", AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"mapping is device local"}, Risk: task.RiskMedium, OccurredAt: now.Add(time.Second), IdempotencyKey: "task-map"})
	if err != nil {
		t.Fatal(err)
	}
	requirement, err := store.GetTaskWorkspace(ctx, "vault-map", created.Task.ID)
	if err != nil || !requirement.Mapped || requirement.Mapping.LocalRoot != sourceRoot || requirement.WorkspaceID != created.Task.WorkspaceID {
		t.Fatalf("requirement=%+v task=%+v error=%v", requirement, created.Task, err)
	}

	confirm := workspacestore.BindInput{WorkspaceID: requirement.WorkspaceID, VaultID: "vault-map", SourceWorkspaceHash: requirement.SourceWorkspaceHash, LocalRoot: sourceRoot, VerifiedBaseline: baseline, ExpectedRevision: 1, OccurredAt: now.Add(2 * time.Second), IdempotencyKey: "confirm-map"}
	confirmed, replayed, err := store.BindWorkspaceMapping(ctx, confirm)
	if err != nil || replayed || confirmed.Revision != 2 {
		t.Fatalf("confirmed=%+v replayed=%v error=%v", confirmed, replayed, err)
	}
	replayedConfirmation, replayed, err := store.BindWorkspaceMapping(ctx, confirm)
	if err != nil || !replayed || replayedConfirmation.LastEventID != confirmed.LastEventID || replayedConfirmation.CreatedEventID != confirmed.CreatedEventID {
		t.Fatalf("replayed=%+v flag=%v error=%v", replayedConfirmation, replayed, err)
	}

	revoked, replayed, err := store.RevokeWorkspaceMapping(ctx, workspacestore.RevokeInput{WorkspaceID: requirement.WorkspaceID, VaultID: "vault-map", ExpectedRevision: 2, OccurredAt: now.Add(3 * time.Second), IdempotencyKey: "revoke-map"})
	if err != nil || replayed || revoked.State != workspacemapping.StateRevoked || revoked.Revision != 3 {
		t.Fatalf("revoked=%+v replayed=%v error=%v", revoked, replayed, err)
	}
	if _, err := store.GetTask(ctx, created.Task.ID); !errors.Is(err, workspacestore.ErrMappingRequired) {
		t.Fatalf("revoked task error=%v", err)
	}

	targetRoot := `D:\talos-private-target-marker`
	remapped, replayed, err := store.BindWorkspaceMapping(ctx, workspacestore.BindInput{WorkspaceID: requirement.WorkspaceID, VaultID: "vault-map", SourceWorkspaceHash: requirement.SourceWorkspaceHash, LocalRoot: targetRoot, VerifiedBaseline: baseline, ExpectedRevision: 3, OccurredAt: now.Add(4 * time.Second), IdempotencyKey: "remap-target"})
	if err != nil || replayed || remapped.State != workspacemapping.StateActive || remapped.Revision != 4 {
		t.Fatalf("remapped=%+v replayed=%v error=%v", remapped, replayed, err)
	}
	mappedTask, err := store.GetTask(ctx, created.Task.ID)
	if err != nil || mappedTask.WorkspaceRoot != targetRoot {
		t.Fatalf("mapped task=%+v error=%v", mappedTask, err)
	}
	if err := store.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("talos-private-source-marker")) || bytes.Contains(data, []byte("talos-private-target-marker")) {
		t.Fatal("workspace root leaked outside encrypted event payloads")
	}
}
