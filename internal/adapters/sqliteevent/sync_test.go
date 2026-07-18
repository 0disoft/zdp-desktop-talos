package sqliteevent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/syncstate"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/syncstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
)

func TestSyncDeviceAndValidatedPackJournalAreDurableAndFailClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "vault.db")
	store := openTestStore(t, databasePath)
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: "vault-sync", RetentionDays: 30, OccurredAt: now, IdempotencyKey: "create-vault-sync"}); err != nil {
		t.Fatal(err)
	}
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	register := syncstore.RegisterDeviceInput{VaultID: "vault-sync", DeviceID: "device-a", PublicKey: public, OccurredAt: now.Add(time.Second), IdempotencyKey: "register-device-a"}
	device, err := store.RegisterSyncDevice(ctx, register)
	if err != nil || device.State != syncstate.DeviceActive || device.Revision != 1 || device.NextSequence != 1 {
		t.Fatalf("device=%+v error=%v", device, err)
	}
	replayedDevice, err := store.RegisterSyncDevice(ctx, register)
	if err != nil || replayedDevice.LastEventID != device.LastEventID {
		t.Fatalf("replayed device=%+v error=%v", replayedDevice, err)
	}
	changed := register
	changed.PublicKey = bytes.Repeat([]byte{8}, ed25519.PublicKeySize)
	if _, err := store.RegisterSyncDevice(ctx, changed); !errors.Is(err, syncstore.ErrIdempotencyConflict) {
		t.Fatalf("changed registration error=%v", err)
	}

	encodedMarker := []byte(`{"schema":"talos.sync-pack/1","private_marker":"must-stay-encrypted"}`)
	record := syncstore.RecordPackInput{PackID: "sha256:" + strings.Repeat("a", 64), VaultID: "vault-sync", DeviceID: "device-a", SequenceStart: 1, SequenceEnd: 2, EventCount: 2, CiphertextHash: strings.Repeat("b", 64), EncodedPack: encodedMarker, ReceivedAt: now.Add(2 * time.Second)}
	receipt, replay, err := store.RecordValidatedSyncPack(ctx, record)
	if err != nil || replay || receipt.State != syncstate.PackValidated {
		t.Fatalf("receipt=%+v replay=%v error=%v", receipt, replay, err)
	}
	replayedReceipt, replay, err := store.RecordValidatedSyncPack(ctx, record)
	if err != nil || !replay || replayedReceipt.LastEventID != receipt.LastEventID {
		t.Fatalf("replayed receipt=%+v replay=%v error=%v", replayedReceipt, replay, err)
	}
	changedPack := record
	changedPack.EncodedPack = []byte("different")
	if _, _, err := store.RecordValidatedSyncPack(ctx, changedPack); !errors.Is(err, syncstore.ErrPackConflict) {
		t.Fatalf("changed pack error=%v", err)
	}
	gap := record
	gap.PackID = "sha256:" + strings.Repeat("c", 64)
	gap.CiphertextHash = strings.Repeat("d", 64)
	gap.SequenceStart, gap.SequenceEnd, gap.EventCount = 4, 4, 1
	if _, _, err := store.RecordValidatedSyncPack(ctx, gap); !errors.Is(err, syncstore.ErrSequenceConflict) {
		t.Fatalf("gap error=%v", err)
	}
	storedReceipt, storedPack, err := store.GetValidatedSyncPack(ctx, record.VaultID, record.PackID)
	if err != nil || storedReceipt != receipt || !bytes.Equal(storedPack, encodedMarker) {
		t.Fatalf("stored receipt=%+v pack=%q error=%v", storedReceipt, storedPack, err)
	}
	if err := store.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	databaseBytes, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(databaseBytes, encodedMarker) || bytes.Contains(databaseBytes, []byte("must-stay-encrypted")) {
		t.Fatal("validated sync pack leaked into SQLite plaintext")
	}

	reopened := openTestStore(t, databasePath)
	defer reopened.Close()
	restoredDevice, err := reopened.GetSyncDevice(ctx, "vault-sync", "device-a")
	if err != nil || restoredDevice.NextSequence != 3 {
		t.Fatalf("restored device=%+v error=%v", restoredDevice, err)
	}
	_, restoredPack, err := reopened.GetValidatedSyncPack(ctx, record.VaultID, record.PackID)
	if err != nil || !bytes.Equal(restoredPack, encodedMarker) {
		t.Fatalf("restored pack=%q error=%v", restoredPack, err)
	}
	revoked, err := reopened.RevokeSyncDevice(ctx, syncstore.RevokeDeviceInput{VaultID: "vault-sync", DeviceID: "device-a", ExpectedRevision: 1, OccurredAt: now.Add(3 * time.Second), IdempotencyKey: "revoke-device-a"})
	if err != nil || revoked.State != syncstate.DeviceRevoked || revoked.Revision != 2 {
		t.Fatalf("revoked=%+v error=%v", revoked, err)
	}
	next := record
	next.PackID = "sha256:" + strings.Repeat("e", 64)
	next.CiphertextHash = strings.Repeat("f", 64)
	next.SequenceStart, next.SequenceEnd, next.EventCount = 3, 3, 1
	if _, _, err := reopened.RecordValidatedSyncPack(ctx, next); !errors.Is(err, syncstore.ErrDeviceRevoked) {
		t.Fatalf("revoked device pack error=%v", err)
	}
}

func TestSchema15MigratesToDurableSyncState(t *testing.T) {
	t.Parallel()
	databasePath := filepath.Join(t.TempDir(), "schema-15.db")
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range migrations {
		if candidate.version > 15 {
			break
		}
		if err := applyMigration(context.Background(), db, candidate); err != nil {
			_ = db.Close()
			t.Fatalf("apply migration %d: %v", candidate.version, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store := openTestStore(t, databasePath)
	defer store.Close()
	var version int
	if err := store.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 16 {
		t.Fatalf("schema version=%d", version)
	}
	for _, table := range []string{"sync_devices", "sync_pack_receipts"} {
		var name string
		if err := store.db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name); err != nil || name != table {
			t.Fatalf("table %s name=%q error=%v", table, name, err)
		}
	}
}
