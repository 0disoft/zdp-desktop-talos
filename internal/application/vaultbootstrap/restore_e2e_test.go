package vaultbootstrap

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/dpapicatalog"
	"github.com/0disoft/zdp-desktop-talos/internal/adapters/localvaultdb"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/artifactstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultbackup"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultcatalog"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
	"github.com/0disoft/zdp-desktop-talos/internal/version"
)

func TestCreatorRestoresBackupOnlyAfterExactConfirmation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	creator, _, _, session, receipt := newRestoreApplicationFixture(t)
	t.Cleanup(func() { _ = session.Close() })
	invalid, err := creator.RestoreBackup(ctx, session, RestoreBackupInput{
		Source: receipt.Path, ExpectedBackupID: receipt.BackupID, ExpectedCiphertextSHA256: receipt.CiphertextSHA256,
		ExpectedRevision: session.Record.Revision, Confirmation: "wrong-vault", ApplicationVersion: version.Application,
	})
	if !errors.Is(err, ErrInvalidInput) || invalid.Session != session || session.database == nil {
		t.Fatalf("invalid result=%+v err=%v", invalid, err)
	}
	restored, err := creator.RestoreBackup(ctx, session, RestoreBackupInput{
		Source: receipt.Path, ExpectedBackupID: receipt.BackupID, ExpectedCiphertextSHA256: receipt.CiphertextSHA256,
		ExpectedRevision: session.Record.Revision, Confirmation: session.Record.ID, ApplicationVersion: version.Application,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Session.Close()
	if restored.Restore.State != vaultbackup.RestoreStateRestored || restored.Restore.BackupID != receipt.BackupID || restored.Session.Record.RetentionDays != 30 || restored.Session.Record.Revision != 1 {
		t.Fatalf("restored=%+v", restored)
	}
}

func TestCreatorReconcilesJournaledRestoreAfterRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	creator, keys, databases, session, receipt := newRestoreApplicationFixture(t)
	catalog := creator.catalog.(vaultcatalog.RestoreJournal)
	key, err := keys.Get(ctx, keyvault.Reference{VaultID: session.Record.ID, KeyID: VaultKeyID})
	if err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	if _, err := databases.StageRestore(ctx, vaultbackup.StageRestoreInput{
		VaultID: session.Record.ID, KeyID: VaultKeyID, Key: key, Source: receipt.Path, ApplicationVersion: version.Application,
		ExpectedBackupID: receipt.BackupID, ExpectedCiphertextSHA256: receipt.CiphertextSHA256,
	}); err != nil {
		t.Fatal(err)
	}
	entry := vaultcatalog.Entry{VaultID: session.Record.ID, CreatedAt: session.Record.CreatedAt, State: vaultcatalog.StateActive}
	if err := catalog.MarkRestorePending(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := creator.ReconcileRestores(ctx, version.Application); err != nil {
		t.Fatal(err)
	}
	opened, err := creator.Open(ctx, entry.VaultID)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if opened.Record.RetentionDays != 30 || opened.Record.Revision != 1 {
		t.Fatalf("opened=%+v", opened.Record)
	}
	if pending, err := catalog.PendingRestores(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
}

func newRestoreApplicationFixture(t *testing.T) (*Creator, *enrollmentKeyStore, *localvaultdb.Factory, *Session, vaultbackup.Receipt) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	keys := &enrollmentKeyStore{values: map[keyvault.Reference][]byte{}}
	catalog, err := dpapicatalog.New(keys)
	if err != nil {
		t.Fatal(err)
	}
	databases, err := localvaultdb.New(filepath.Join(root, "vaults"))
	if err != nil {
		t.Fatal(err)
	}
	creator, err := NewCreator(keys, databases, catalog, databases)
	if err != nil {
		t.Fatal(err)
	}
	session, err := creator.Create(ctx, CreateInput{RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.database.PutArtifact(ctx, artifactstore.PutInput{
		VaultID: session.Record.ID, SchemaVersion: 1, Sensitivity: event.SensitivitySensitive,
		ContentType: "application/octet-stream", Payload: []byte("application-restore-artifact"),
	}); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(root, "application.talos-backup")
	receipt, err := session.CreateBackup(ctx, backupPath, version.Application)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := session.UpdateRetention(ctx, UpdateRetentionInput{
		ExpectedRevision: session.Record.Revision, RetentionDays: 90, IdempotencyKey: "restore-live-update",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.RetentionDays != 90 {
		t.Fatalf("updated=%+v", updated)
	}
	if _, err := session.database.GetVault(ctx, session.Record.ID); errors.Is(err, vaultstore.ErrNotFound) {
		t.Fatal(err)
	}
	return creator, keys, databases, session, receipt
}
