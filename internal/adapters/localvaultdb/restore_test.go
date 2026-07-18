package localvaultdb

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/artifactstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultbackup"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultdb"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
)

var _ vaultbackup.Restorer = (*Factory)(nil)

func TestRestoreReplacesLiveGenerationAndPreservesReceiptIdentity(t *testing.T) {
	t.Parallel()
	fixture := newRestoreFixture(t)
	staged := fixture.stage(t)
	fixture.close(t)
	restored, err := fixture.factory.ReconcileRestore(context.Background(), fixture.reconcileInput())
	if err != nil {
		t.Fatal(err)
	}
	if restored.Schema != vaultbackup.RestoreSchema || restored.State != vaultbackup.RestoreStateRestored || restored.BackupID != staged.BackupID || restored.CiphertextSHA256 != staged.CiphertextSHA256 || restored.EventCount != staged.EventCount || restored.ArtifactCount != 1 {
		t.Fatalf("restore=%+v staged=%+v", restored, staged)
	}
	if err := fixture.factory.FinalizeRestore(context.Background(), fixture.vaultID); err != nil {
		t.Fatal(err)
	}
	fixture.assertLiveRetention(t, 30)
	paths, _ := fixture.factory.restorePaths(fixture.vaultID)
	for _, path := range []string{paths.stageRoot, paths.previousRoot} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("restore residue %s: %v", path, err)
		}
	}
}

func TestRestoreResumesAfterEveryGenerationSwapCrashPoint(t *testing.T) {
	t.Parallel()
	for _, step := range []string{"previous_database_saved", "previous_blobs_saved", "restored_database_activated", "restored_blobs_activated"} {
		step := step
		t.Run(step, func(t *testing.T) {
			t.Parallel()
			fixture := newRestoreFixture(t)
			fixture.stage(t)
			fixture.close(t)
			crashed := false
			fixture.factory.restoreHook = func(current string) error {
				if current == step && !crashed {
					crashed = true
					return errors.New("simulated process interruption")
				}
				return nil
			}
			if _, err := fixture.factory.ReconcileRestore(context.Background(), fixture.reconcileInput()); !errors.Is(err, vaultbackup.ErrRestoreIncomplete) || !crashed {
				t.Fatalf("first reconcile err=%v crashed=%v", err, crashed)
			}
			resumed, err := New(fixture.root)
			if err != nil {
				t.Fatal(err)
			}
			result, err := resumed.ReconcileRestore(context.Background(), fixture.reconcileInput())
			if err != nil || result.State != vaultbackup.RestoreStateRestored {
				t.Fatalf("resumed result=%+v err=%v", result, err)
			}
			if err := resumed.FinalizeRestore(context.Background(), fixture.vaultID); err != nil {
				t.Fatal(err)
			}
			fixture.factory = resumed
			fixture.assertLiveRetention(t, 30)
		})
	}
}

func TestRestoreRollsBackCorruptPromotedGeneration(t *testing.T) {
	t.Parallel()
	fixture := newRestoreFixture(t)
	fixture.stage(t)
	fixture.close(t)
	paths, _ := fixture.factory.restorePaths(fixture.vaultID)
	if err := os.WriteFile(paths.stageDatabase, []byte("corrupt staged database"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.factory.ReconcileRestore(context.Background(), fixture.reconcileInput())
	if err != nil {
		t.Fatal(err)
	}
	if result.State != vaultbackup.RestoreStateRolledBack {
		t.Fatalf("result=%+v", result)
	}
	if err := fixture.factory.FinalizeRestore(context.Background(), fixture.vaultID); err != nil {
		t.Fatal(err)
	}
	fixture.assertLiveRetention(t, 90)
}

func TestRestoreRejectsChangedBackupIdentityAndAuthenticatedMetadataTamper(t *testing.T) {
	t.Parallel()
	fixture := newRestoreFixture(t)
	wrong := fixture.stageInput()
	wrong.ExpectedCiphertextSHA256 = stringsOf("f", 64)
	if _, err := fixture.factory.StageRestore(context.Background(), wrong); !errors.Is(err, vaultbackup.ErrCorrupt) {
		t.Fatalf("changed identity error=%v", err)
	}
	staged := fixture.stage(t)
	fixture.close(t)
	paths, _ := fixture.factory.restorePaths(fixture.vaultID)
	metadataPath := filepath.Join(paths.stageRoot, restoreMetadataName)
	encoded, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	encoded[len(encoded)/2] ^= 1
	if err := os.WriteFile(metadataPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.factory.ReconcileRestore(context.Background(), fixture.reconcileInput()); !errors.Is(err, vaultbackup.ErrRestoreIncomplete) {
		t.Fatalf("metadata tamper error=%v staged=%+v", err, staged)
	}
	fixture.assertLiveRetention(t, 90)
}

type restoreFixture struct {
	root       string
	factory    *Factory
	database   vaultdb.Database
	vaultID    string
	key        []byte
	backupPath string
	receipt    vaultbackup.Receipt
}

func newRestoreFixture(t *testing.T) *restoreFixture {
	t.Helper()
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "vaults")
	factory, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 8, 9, 10, 0, time.UTC)
	factory.now = func() time.Time { return now.Add(time.Minute) }
	factory.random = bytes.NewReader(bytes.Repeat([]byte{0x6a}, 256))
	vaultID := "00000000-0000-7000-8000-0000000000e1"
	key := bytes.Repeat([]byte{0x5a}, 32)
	database, err := factory.Create(ctx, vaultID, "vault-kek-v1", key)
	if err != nil {
		t.Fatal(err)
	}
	created, err := database.CreateVault(ctx, vaultstore.CreateInput{VaultID: vaultID, RetentionDays: 30, OccurredAt: now, IdempotencyKey: "restore-vault"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.PutArtifact(ctx, artifactstore.PutInput{VaultID: vaultID, SchemaVersion: 1, Sensitivity: event.SensitivitySensitive, ContentType: "application/octet-stream", Payload: []byte("restore-artifact-marker")}); err != nil {
		t.Fatal(err)
	}
	backupDirectory := t.TempDir()
	backupPath := filepath.Join(backupDirectory, "restore.talos-backup")
	receipt, err := factory.CreateBackup(ctx, vaultbackup.CreateInput{VaultID: vaultID, KeyID: "vault-kek-v1", Key: key, Destination: backupPath, ApplicationVersion: "0.31.1", CreatedAt: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateVaultRetention(ctx, vaultstore.UpdateRetentionInput{VaultID: vaultID, ExpectedRevision: created.Revision, RetentionDays: 90, OccurredAt: now.Add(2 * time.Second), IdempotencyKey: "restore-live-newer"}); err != nil {
		t.Fatal(err)
	}
	return &restoreFixture{root: root, factory: factory, database: database, vaultID: vaultID, key: key, backupPath: backupPath, receipt: receipt}
}

func (f *restoreFixture) stageInput() vaultbackup.StageRestoreInput {
	return vaultbackup.StageRestoreInput{
		VaultID: f.vaultID, KeyID: "vault-kek-v1", Key: f.key, Source: f.backupPath, ApplicationVersion: "0.31.1",
		ExpectedBackupID: f.receipt.BackupID, ExpectedCiphertextSHA256: f.receipt.CiphertextSHA256,
	}
}

func (f *restoreFixture) stage(t *testing.T) vaultbackup.Preflight {
	t.Helper()
	staged, err := f.factory.StageRestore(context.Background(), f.stageInput())
	if err != nil {
		t.Fatal(err)
	}
	return staged
}

func (f *restoreFixture) reconcileInput() vaultbackup.ReconcileRestoreInput {
	return vaultbackup.ReconcileRestoreInput{VaultID: f.vaultID, KeyID: "vault-kek-v1", Key: f.key, ApplicationVersion: "0.31.1"}
}

func (f *restoreFixture) close(t *testing.T) {
	t.Helper()
	if f.database == nil {
		return
	}
	if err := f.database.Close(); err != nil {
		t.Fatal(err)
	}
	f.database = nil
}

func (f *restoreFixture) assertLiveRetention(t *testing.T, retention int) {
	t.Helper()
	database, err := f.factory.Open(context.Background(), f.vaultID, "vault-kek-v1", f.key)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	record, err := database.GetVault(context.Background(), f.vaultID)
	if err != nil || record.RetentionDays != retention {
		t.Fatalf("record=%+v err=%v want retention=%d", record, err, retention)
	}
}

func stringsOf(value string, count int) string {
	return string(bytes.Repeat([]byte(value), count))
}
