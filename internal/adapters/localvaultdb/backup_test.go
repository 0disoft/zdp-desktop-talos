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
	"github.com/0disoft/zdp-desktop-talos/internal/ports/eventstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultbackup"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
)

var _ vaultbackup.Store = (*Factory)(nil)

func TestEncryptedBackupRoundTripAndMigrationPreflight(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	factory, err := New(filepath.Join(t.TempDir(), "vaults"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 2, 3, 4, 0, time.UTC)
	factory.now = func() time.Time { return now.Add(time.Minute) }
	factory.random = bytes.NewReader(bytes.Repeat([]byte{0x75}, 128))
	vaultID := "00000000-0000-7000-8000-0000000000c1"
	key := bytes.Repeat([]byte{0x2d}, 32)
	db, err := factory.Create(ctx, vaultID, "vault-kek-v1", key)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.CreateVault(ctx, vaultstore.CreateInput{VaultID: vaultID, RetentionDays: 45, OccurredAt: now, IdempotencyKey: "vault"}); err != nil {
		t.Fatal(err)
	}
	concrete := db.(*database)
	if _, err := concrete.Append(ctx, eventstore.AppendInput{VaultID: vaultID, Type: "backup.fixture", SchemaVersion: 1, Sensitivity: event.SensitivityPrivate, Payload: []byte("event-backup-private-marker"), OccurredAt: now.Add(time.Second), IdempotencyKey: "event"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PutArtifact(ctx, artifactstore.PutInput{VaultID: vaultID, SchemaVersion: 1, Sensitivity: event.SensitivitySensitive, ContentType: "application/octet-stream", Payload: []byte("artifact-backup-private-marker")}); err != nil {
		t.Fatal(err)
	}

	backupPath := filepath.Join(t.TempDir(), "vault-alpha.talos-backup")
	receipt, err := factory.CreateBackup(ctx, vaultbackup.CreateInput{VaultID: vaultID, KeyID: "vault-kek-v1", Key: key, Destination: backupPath, ApplicationVersion: "0.31.0", CreatedAt: now.Add(2 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Schema != vaultbackup.ReceiptSchema || receipt.VaultID != vaultID || receipt.Path != backupPath || receipt.SourceSchemaVersion != 25 || receipt.EventCount != 2 || receipt.ArtifactCount != 1 || receipt.EncryptedSizeBytes <= 0 || len(receipt.CiphertextSHA256) != 64 {
		t.Fatalf("unexpected backup receipt: %+v", receipt)
	}
	encrypted, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{[]byte(vaultID), []byte("event-backup-private-marker"), []byte("artifact-backup-private-marker"), []byte(`"schema":"talos.vault-backup-manifest/1"`)} {
		if bytes.Contains(encrypted, marker) {
			t.Fatalf("encrypted backup exposes marker %q", marker)
		}
	}

	preflight, err := factory.PreflightBackup(ctx, vaultbackup.PreflightInput{VaultID: vaultID, KeyID: "vault-kek-v1", Key: key, Source: backupPath, ApplicationVersion: "0.31.0"})
	if err != nil {
		t.Fatal(err)
	}
	if preflight.Schema != vaultbackup.PreflightSchema || preflight.BackupID != receipt.BackupID || preflight.SourceSchemaVersion != 25 || preflight.TargetSchemaVersion != 25 || preflight.MigrationRequired || preflight.EventCount != 2 || preflight.ArtifactCount != 1 || preflight.CiphertextSHA256 != receipt.CiphertextSHA256 {
		t.Fatalf("unexpected backup preflight: %+v", preflight)
	}
}

func TestBackupFailsClosedOnConflictWrongVaultWrongKeyAndTamper(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	factory, err := New(filepath.Join(t.TempDir(), "vaults"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 3, 4, 5, 0, time.UTC)
	factory.now = func() time.Time { return now }
	factory.random = bytes.NewReader(bytes.Repeat([]byte{0x64}, 128))
	vaultID := "00000000-0000-7000-8000-0000000000d1"
	key := bytes.Repeat([]byte{0x44}, 32)
	db, err := factory.Create(ctx, vaultID, "vault-kek-v1", key)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.CreateVault(ctx, vaultstore.CreateInput{VaultID: vaultID, RetentionDays: 30, OccurredAt: now, IdempotencyKey: "vault"}); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(t.TempDir(), "vault.talos-backup")
	input := vaultbackup.CreateInput{VaultID: vaultID, KeyID: "vault-kek-v1", Key: key, Destination: backupPath, ApplicationVersion: "0.31.0", CreatedAt: now}
	if _, err := factory.CreateBackup(ctx, input); err != nil {
		t.Fatal(err)
	}
	if _, err := factory.CreateBackup(ctx, input); !errors.Is(err, vaultbackup.ErrAlreadyExists) {
		t.Fatalf("duplicate backup error = %v", err)
	}

	checks := []struct {
		name  string
		input vaultbackup.PreflightInput
		want  error
	}{
		{name: "wrong Vault", input: vaultbackup.PreflightInput{VaultID: "00000000-0000-7000-8000-0000000000d2", KeyID: "vault-kek-v1", Key: key, Source: backupPath, ApplicationVersion: "0.31.0"}, want: vaultbackup.ErrWrongVault},
		{name: "wrong key", input: vaultbackup.PreflightInput{VaultID: vaultID, KeyID: "vault-kek-v1", Key: bytes.Repeat([]byte{0x45}, 32), Source: backupPath, ApplicationVersion: "0.31.0"}, want: vaultbackup.ErrCorrupt},
		{name: "relative path", input: vaultbackup.PreflightInput{VaultID: vaultID, KeyID: "vault-kek-v1", Key: key, Source: "vault.talos-backup", ApplicationVersion: "0.31.0"}, want: vaultbackup.ErrUnsafePath},
	}
	for _, check := range checks {
		check := check
		t.Run(check.name, func(t *testing.T) {
			t.Parallel()
			if _, err := factory.PreflightBackup(ctx, check.input); !errors.Is(err, check.want) {
				t.Fatalf("error = %v, want %v", err, check.want)
			}
		})
	}

	tamperedPath := filepath.Join(filepath.Dir(backupPath), "tampered.talos-backup")
	tampered, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	tampered[len(tampered)/2] ^= 0x01
	if err := os.WriteFile(tamperedPath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := factory.PreflightBackup(ctx, vaultbackup.PreflightInput{VaultID: vaultID, KeyID: "vault-kek-v1", Key: key, Source: tamperedPath, ApplicationVersion: "0.31.0"}); !errors.Is(err, vaultbackup.ErrCorrupt) {
		t.Fatalf("tampered backup error = %v", err)
	}
}
