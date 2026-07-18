package sqliteevent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/syncstate"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/eventstore"
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

func TestSyncExportReservationAndReadyPackAreRestartSafe(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "export.db")
	store := openTestStore(t, databasePath)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: "vault-export", RetentionDays: 30, OccurredAt: now, IdempotencyKey: "create-vault-export"}); err != nil {
		t.Fatal(err)
	}
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterSyncDevice(ctx, syncstore.RegisterDeviceInput{VaultID: "vault-export", DeviceID: "device-local", PublicKey: public, OccurredAt: now.Add(time.Second), IdempotencyKey: "register-local"}); err != nil {
		t.Fatal(err)
	}
	markers := [][]byte{[]byte(`{"item":"first-private-marker"}`), []byte(`{"item":"second-private-marker"}`)}
	for index, marker := range markers {
		if _, err := store.Append(ctx, eventstore.AppendInput{VaultID: "vault-export", Type: "task.outcome.observed", SchemaVersion: 1, Sensitivity: event.SensitivityPrivate, Payload: marker, OccurredAt: now.Add(time.Duration(index+2) * time.Second), IdempotencyKey: fmt.Sprintf("export-event-%d", index)}); err != nil {
			t.Fatal(err)
		}
	}
	prepared, replay, err := store.PrepareSyncExport(ctx, syncstore.PrepareExportInput{VaultID: "vault-export", DeviceID: "device-local", Limit: 3, OccurredAt: now.Add(5 * time.Second)})
	if err != nil || replay || prepared.Batch.State != syncstate.ExportPreparing || prepared.Batch.SequenceStart != 1 || prepared.Batch.SequenceEnd != 3 || len(prepared.Events) != 3 {
		t.Fatalf("prepared=%+v replay=%v error=%v", prepared, replay, err)
	}
	replayed, replay, err := store.PrepareSyncExport(ctx, syncstore.PrepareExportInput{VaultID: "vault-export", DeviceID: "device-local", Limit: 1, OccurredAt: now.Add(6 * time.Second)})
	if err != nil || !replay || replayed.Batch.ExportID != prepared.Batch.ExportID || len(replayed.Events) != 3 {
		t.Fatalf("replayed=%+v replay=%v error=%v", replayed, replay, err)
	}
	encoded := []byte(`{"schema":"talos.sync-pack/1","private_marker":"ready-pack"}`)
	finalize := syncstore.FinalizeExportInput{ExportID: prepared.Batch.ExportID, VaultID: "vault-export", DeviceID: "device-local", PackID: "sha256:" + strings.Repeat("a", 64), SequenceStart: 1, SequenceEnd: 3, EventCount: 3, CiphertextHash: strings.Repeat("b", 64), EncodedPack: encoded, OccurredAt: now.Add(7 * time.Second)}
	ready, stored, replay, err := store.FinalizeSyncExport(ctx, finalize)
	if err != nil || replay || ready.State != syncstate.ExportReady || !bytes.Equal(stored, encoded) {
		t.Fatalf("ready=%+v stored=%q replay=%v error=%v", ready, stored, replay, err)
	}
	readyAgain, storedAgain, replay, err := store.FinalizeSyncExport(ctx, finalize)
	if err != nil || !replay || readyAgain.ExportID != ready.ExportID || !bytes.Equal(storedAgain, encoded) {
		t.Fatalf("readyAgain=%+v stored=%q replay=%v error=%v", readyAgain, storedAgain, replay, err)
	}
	if _, _, err := store.PrepareSyncExport(ctx, syncstore.PrepareExportInput{VaultID: "vault-export", DeviceID: "device-local", Limit: 2, OccurredAt: now.Add(8 * time.Second)}); !errors.Is(err, syncstore.ErrNoExportableEvents) {
		t.Fatalf("empty export error=%v", err)
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
	for _, marker := range append(markers, encoded) {
		if bytes.Contains(databaseBytes, marker) {
			t.Fatalf("sync export plaintext leaked: %q", marker)
		}
	}
	reopened := openTestStore(t, databasePath)
	defer reopened.Close()
	restored, restoredBytes, err := reopened.GetSyncExport(ctx, "vault-export", ready.ExportID)
	if err != nil || restored != ready || !bytes.Equal(restoredBytes, encoded) {
		t.Fatalf("restored=%+v bytes=%q error=%v", restored, restoredBytes, err)
	}
	if _, err := reopened.Append(ctx, eventstore.AppendInput{VaultID: "vault-export", Type: "memory.reviewed", SchemaVersion: 1, Sensitivity: event.SensitivityPrivate, Payload: []byte(`{"item":"third"}`), OccurredAt: now.Add(9 * time.Second), IdempotencyKey: "export-event-3"}); err != nil {
		t.Fatal(err)
	}
	next, replay, err := reopened.PrepareSyncExport(ctx, syncstore.PrepareExportInput{VaultID: "vault-export", DeviceID: "device-local", Limit: 2, OccurredAt: now.Add(10 * time.Second)})
	if err != nil || replay || next.Batch.SequenceStart != 4 || next.Batch.SequenceEnd != 4 || len(next.Events) != 1 {
		t.Fatalf("next=%+v replay=%v error=%v", next, replay, err)
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
	if version != 17 {
		t.Fatalf("schema version=%d", version)
	}
	for _, table := range []string{"sync_devices", "sync_pack_receipts", "sync_event_origins", "sync_export_heads", "sync_export_batches", "sync_export_batch_events"} {
		var name string
		if err := store.db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name); err != nil || name != table {
			t.Fatalf("table %s name=%q error=%v", table, name, err)
		}
	}
}
