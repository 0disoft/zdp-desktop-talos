package doctor

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/localvaultdb"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/artifactstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultbackup"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
	"github.com/0disoft/zdp-desktop-talos/internal/version"
)

const (
	doctorBackupVaultID = "00000000-0000-7000-8000-0000000000d1"
	doctorBackupKeyID   = "vault-kek-v1"
)

func backupCheck(ctx context.Context) (Check, error) {
	if ctx == nil {
		return Check{}, fmt.Errorf("backup self-test context is nil")
	}
	directory, err := os.MkdirTemp("", "talos-backup-doctor-")
	if err != nil {
		return Check{}, fmt.Errorf("create backup self-test directory: %w", err)
	}
	defer os.RemoveAll(directory)

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return Check{}, fmt.Errorf("generate backup self-test key: %w", err)
	}
	defer clear(key)

	factory, err := localvaultdb.New(filepath.Join(directory, "vaults"))
	if err != nil {
		return Check{}, fmt.Errorf("initialize backup self-test storage: %w", err)
	}
	database, err := factory.Create(ctx, doctorBackupVaultID, doctorBackupKeyID, key)
	if err != nil {
		return Check{}, fmt.Errorf("create backup self-test Vault: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = database.Close()
		}
	}()

	now := time.Now().UTC()
	created, err := database.CreateVault(ctx, vaultstore.CreateInput{
		VaultID: doctorBackupVaultID, RetentionDays: 30, IdempotencyKey: "doctor-backup-vault-create", OccurredAt: now,
	})
	if err != nil {
		return Check{}, fmt.Errorf("initialize backup self-test Vault: %w", err)
	}
	artifactMarker := []byte("talos-doctor-backup-artifact-private-marker")
	if _, err := database.PutArtifact(ctx, artifactstore.PutInput{
		VaultID: doctorBackupVaultID, SchemaVersion: 1, Sensitivity: event.SensitivitySensitive,
		ContentType: "application/octet-stream", Payload: artifactMarker,
	}); err != nil {
		return Check{}, fmt.Errorf("store backup self-test artifact: %w", err)
	}

	backupDirectory := filepath.Join(directory, "backups")
	if err := os.Mkdir(backupDirectory, 0o700); err != nil {
		return Check{}, fmt.Errorf("create backup self-test destination: %w", err)
	}
	backupPath := filepath.Join(backupDirectory, "doctor"+vaultbackup.FileExtension)
	receipt, err := factory.CreateBackup(ctx, vaultbackup.CreateInput{
		VaultID: doctorBackupVaultID, KeyID: doctorBackupKeyID, Key: key,
		Destination: backupPath, ApplicationVersion: version.Application, CreatedAt: now.Add(2 * time.Millisecond),
	})
	if err != nil {
		return Check{}, fmt.Errorf("create encrypted backup self-test file: %w", err)
	}
	ciphertext, err := os.ReadFile(backupPath)
	if err != nil {
		return Check{}, fmt.Errorf("read encrypted backup self-test file: %w", err)
	}
	if bytes.Contains(ciphertext, artifactMarker) || bytes.Contains(ciphertext, []byte(doctorBackupVaultID)) {
		return Check{}, fmt.Errorf("plaintext marker found in encrypted backup")
	}

	updated, err := database.UpdateVaultRetention(ctx, vaultstore.UpdateRetentionInput{
		VaultID: doctorBackupVaultID, ExpectedRevision: created.Revision, RetentionDays: 60,
		OccurredAt: now.Add(3 * time.Millisecond), IdempotencyKey: "doctor-post-backup-retention",
	})
	if err != nil {
		return Check{}, fmt.Errorf("mutate live Vault after backup: %w", err)
	}
	preflight, err := factory.PreflightBackup(ctx, vaultbackup.PreflightInput{
		VaultID: doctorBackupVaultID, KeyID: doctorBackupKeyID, Key: key,
		Source: backupPath, ApplicationVersion: version.Application,
	})
	if err != nil {
		return Check{}, fmt.Errorf("run isolated backup self-test preflight: %w", err)
	}
	liveRecord, err := database.GetVault(ctx, doctorBackupVaultID)
	if err != nil || liveRecord != updated || liveRecord.RetentionDays != 60 {
		return Check{}, fmt.Errorf("isolated backup preflight changed the live Vault")
	}
	if receipt.Schema != vaultbackup.ReceiptSchema || preflight.Schema != vaultbackup.PreflightSchema ||
		receipt.BackupID != preflight.BackupID || receipt.VaultID != doctorBackupVaultID || preflight.VaultID != doctorBackupVaultID ||
		receipt.CiphertextSHA256 != preflight.CiphertextSHA256 || receipt.EncryptedSizeBytes != preflight.EncryptedSizeBytes ||
		receipt.EventCount != preflight.EventCount ||
		preflight.TargetApplicationVersion != version.Application || preflight.TargetSchemaVersion < preflight.SourceSchemaVersion ||
		preflight.ArtifactCount != 1 || preflight.EventCount <= 0 {
		return Check{}, fmt.Errorf("backup self-test receipt and preflight disagree")
	}
	if _, err := factory.StageRestore(ctx, vaultbackup.StageRestoreInput{
		VaultID: doctorBackupVaultID, KeyID: doctorBackupKeyID, Key: key, Source: backupPath,
		ApplicationVersion: version.Application, ExpectedBackupID: receipt.BackupID,
		ExpectedCiphertextSHA256: receipt.CiphertextSHA256,
	}); err != nil {
		return Check{}, fmt.Errorf("stage journaled backup self-test restore: %w", err)
	}
	if err := database.Close(); err != nil {
		return Check{}, fmt.Errorf("close backup self-test Vault: %w", err)
	}
	closed = true
	restored, err := factory.ReconcileRestore(ctx, vaultbackup.ReconcileRestoreInput{
		VaultID: doctorBackupVaultID, KeyID: doctorBackupKeyID, Key: key, ApplicationVersion: version.Application,
	})
	if err != nil {
		return Check{}, fmt.Errorf("activate journaled backup self-test restore: %w", err)
	}
	if restored.State != vaultbackup.RestoreStateRestored || restored.BackupID != receipt.BackupID {
		return Check{}, fmt.Errorf("activate journaled backup self-test restore: unexpected state %s", restored.State)
	}
	if err := factory.FinalizeRestore(ctx, doctorBackupVaultID); err != nil {
		return Check{}, fmt.Errorf("finalize backup self-test restore: %w", err)
	}
	reopened, err := factory.Open(ctx, doctorBackupVaultID, doctorBackupKeyID, key)
	if err != nil {
		return Check{}, fmt.Errorf("open restored backup self-test Vault: %w", err)
	}
	restoredRecord, recordErr := reopened.GetVault(ctx, doctorBackupVaultID)
	closeErr := reopened.Close()
	if recordErr != nil || closeErr != nil || restoredRecord.RetentionDays != created.RetentionDays || restoredRecord.Revision != created.Revision {
		return Check{}, fmt.Errorf("restored backup self-test Vault differs from snapshot: record_error=%v close_error=%v", recordErr, closeErr)
	}

	return Check{Name: "vault_backup", Status: "passed", Details: map[string]any{
		"online_snapshot":         true,
		"artifact_count":          preflight.ArtifactCount,
		"event_count":             preflight.EventCount,
		"encrypted_bytes":         preflight.EncryptedSizeBytes,
		"plaintext_marker_absent": true,
		"isolated_preflight":      true,
		"live_vault_unchanged":    true,
		"journaled_live_restore":  true,
		"restored_revision":       restoredRecord.Revision,
		"source_schema":           preflight.SourceSchemaVersion,
		"target_schema":           preflight.TargetSchemaVersion,
	}}, nil
}
