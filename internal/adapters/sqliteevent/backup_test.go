package sqliteevent

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/artifactstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/eventstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
	"github.com/0disoft/zdp-desktop-talos/internal/security/envelope"
)

func TestOnlineBackupIncludesWALStateAndEncryptedArtifacts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source.db")
	backupPath := filepath.Join(root, "backup.db")
	key := bytes.Repeat([]byte{0x5c}, 32)
	sealer, err := envelope.NewSealer("vault-kek-v1", key)
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(sourcePath, sealer)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	vaultID := "00000000-0000-7000-8000-0000000000b1"
	now := time.Date(2026, 7, 19, 1, 2, 3, 0, time.UTC)
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: vaultID, RetentionDays: 30, OccurredAt: now, IdempotencyKey: "vault"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, eventstore.AppendInput{VaultID: vaultID, Type: "backup.fixture", SchemaVersion: 1, Sensitivity: event.SensitivityPrivate, Payload: []byte("event-private-marker"), OccurredAt: now.Add(time.Second), IdempotencyKey: "event"}); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.PutArtifact(ctx, artifactstore.PutInput{VaultID: vaultID, SchemaVersion: 1, Sensitivity: event.SensitivitySensitive, ContentType: "application/octet-stream", Payload: []byte("artifact-private-marker")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.OnlineBackup(ctx, backupPath); err != nil {
		t.Fatal(err)
	}

	info, err := InspectSnapshot(ctx, backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.SchemaVersion != CurrentSchemaVersion() || info.VaultID != vaultID || len(info.Artifacts) != 1 || info.Artifacts[0].ArtifactID != artifact.ID {
		t.Fatalf("unexpected snapshot info: %+v", info)
	}
	backupSealer, err := envelope.NewSealer("vault-kek-v1", key)
	if err != nil {
		t.Fatal(err)
	}
	backupStore, err := Open(backupPath, backupSealer)
	if err != nil {
		t.Fatal(err)
	}
	defer backupStore.Close()
	report, err := backupStore.ValidateIntegrity(ctx, vaultID)
	if err == nil {
		t.Fatal("integrity validation ignored the missing backup blob")
	}
	if report != (IntegrityReport{}) {
		t.Fatalf("failed integrity validation returned a partial report: %+v", report)
	}

	if err := os.MkdirAll(backupPath+".blobs", 0o700); err != nil {
		t.Fatal(err)
	}
	sourceBlob := filepath.Join(sourcePath+".blobs", info.Artifacts[0].StorageName)
	backupBlob := filepath.Join(backupPath+".blobs", info.Artifacts[0].StorageName)
	payload, err := os.ReadFile(sourceBlob)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupBlob, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err = backupStore.ValidateIntegrity(ctx, vaultID)
	if err != nil {
		t.Fatal(err)
	}
	if report.EventCount != 2 || report.ArtifactCount != 1 {
		t.Fatalf("unexpected integrity report: %+v", report)
	}
}
