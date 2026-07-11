package sqliteevent

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/artifactstore"
	"github.com/0disoft/zdp-desktop-talos/internal/security/envelope"
)

func TestArtifactRoundTripSurvivesRestartWithoutPlaintext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "vault.db")
	sealer, err := envelope.NewSealer("test-key", bytes.Repeat([]byte{0x31}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(databasePath, sealer)
	if err != nil {
		t.Fatal(err)
	}
	marker := []byte("artifact-private-marker-must-stay-encrypted")
	record, err := store.PutArtifact(ctx, artifactstore.PutInput{
		VaultID: "vault-artifact", SchemaVersion: 1, Sensitivity: event.SensitivitySensitive,
		ContentType: "text/plain; charset=utf-8", Payload: marker,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(databasePath + ".blobs")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".blob" {
		t.Fatalf("unexpected blob directory entries: %+v", entries)
	}
	ciphertext, err := os.ReadFile(filepath.Join(databasePath+".blobs", entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, marker) {
		t.Fatal("artifact staging stored plaintext")
	}
	reopened, err := Open(databasePath, sealer)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored, payload, err := reopened.GetArtifact(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored != record || !bytes.Equal(payload, marker) {
		t.Fatalf("restored=%+v payload=%q", restored, payload)
	}
}

func TestArtifactReconciliationPromotesDurableStage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "vault.db")
	store := openTestStore(t, databasePath)
	record, err := store.PutArtifact(ctx, artifactstore.PutInput{
		VaultID: "vault-artifact", SchemaVersion: 1, Sensitivity: event.SensitivityPrivate,
		ContentType: "application/octet-stream", Payload: []byte("recoverable artifact"),
	})
	if err != nil {
		t.Fatal(err)
	}
	row, err := store.readArtifact(ctx, record.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE artifacts SET state = 'staged' WHERE artifact_id = ?`, record.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(store.blobRoot, row.storageName), filepath.Join(store.blobRoot, row.stagingName)); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTestStore(t, databasePath)
	defer reopened.Close()
	if _, payload, err := reopened.GetArtifact(ctx, record.ID); err != nil || string(payload) != "recoverable artifact" {
		t.Fatalf("payload=%q err=%v", payload, err)
	}
	if _, err := os.Stat(filepath.Join(reopened.blobRoot, row.stagingName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging file survived reconciliation: %v", err)
	}
}

func TestArtifactTamperingAndSecretPersistenceFailClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	defer store.Close()
	if _, err := store.PutArtifact(ctx, artifactstore.PutInput{
		VaultID: "vault-artifact", SchemaVersion: 1, Sensitivity: event.SensitivitySecret,
		ContentType: "text/plain", Payload: []byte("forbidden"),
	}); !errors.Is(err, artifactstore.ErrInvalidInput) {
		t.Fatalf("secret artifact error=%v", err)
	}
	if _, err := store.PutArtifact(ctx, artifactstore.PutInput{
		VaultID: "vault-artifact", SchemaVersion: 1, Sensitivity: event.SensitivityPrivate,
		ContentType: "text/plain\r\nmalformed", Payload: []byte("invalid metadata"),
	}); !errors.Is(err, artifactstore.ErrInvalidInput) {
		t.Fatalf("malformed content type error=%v", err)
	}
	record, err := store.PutArtifact(ctx, artifactstore.PutInput{
		VaultID: "vault-artifact", SchemaVersion: 1, Sensitivity: event.SensitivityPrivate,
		ContentType: "text/plain", Payload: []byte("tamper target"),
	})
	if err != nil {
		t.Fatal(err)
	}
	row, err := store.readArtifact(ctx, record.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.blobRoot, row.storageName)
	ciphertext, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext[len(ciphertext)-1] ^= 0xff
	if err := os.WriteFile(path, ciphertext, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.GetArtifact(ctx, record.ID); !errors.Is(err, artifactstore.ErrCorrupt) {
		t.Fatalf("tampered artifact error=%v", err)
	}
}

func TestArtifactOversizedCiphertextFailsBeforeAllocation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	defer store.Close()
	record, err := store.PutArtifact(ctx, artifactstore.PutInput{
		VaultID: "vault-artifact", SchemaVersion: 1, Sensitivity: event.SensitivityPrivate,
		ContentType: "application/octet-stream", Payload: []byte("bounded"),
	})
	if err != nil {
		t.Fatal(err)
	}
	row, err := store.readArtifact(ctx, record.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(filepath.Join(store.blobRoot, row.storageName), maxCiphertextBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.GetArtifact(ctx, record.ID); !errors.Is(err, artifactstore.ErrCorrupt) {
		t.Fatalf("oversized ciphertext error=%v", err)
	}
}

func TestArtifactReconciliationRemovesUnownedStageAndAbandonedRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "vault.db")
	store := openTestStore(t, databasePath)
	record, err := store.PutArtifact(ctx, artifactstore.PutInput{
		VaultID: "vault-artifact", SchemaVersion: 1, Sensitivity: event.SensitivityPrivate,
		ContentType: "text/plain", Payload: []byte("abandoned"),
	})
	if err != nil {
		t.Fatal(err)
	}
	row, err := store.readArtifact(ctx, record.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE artifacts SET state = 'staged' WHERE artifact_id = ?`, record.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(store.blobRoot, row.storageName)); err != nil {
		t.Fatal(err)
	}
	orphanName := "00000000-0000-7000-8000-000000000099.stage"
	if err := os.WriteFile(filepath.Join(store.blobRoot, orphanName), []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTestStore(t, databasePath)
	defer reopened.Close()
	if _, _, err := reopened.GetArtifact(ctx, record.ID); !errors.Is(err, artifactstore.ErrNotFound) {
		t.Fatalf("abandoned artifact error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(reopened.blobRoot, orphanName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan staging file survived: %v", err)
	}
}
