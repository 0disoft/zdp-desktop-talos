package sqliteevent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/decision"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/syncstate"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/decisionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/eventstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorystore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/syncstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
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
	eventTypes := []string{taskContractCreatedEventType, memoryCandidateCreatedEventType}
	for index, marker := range markers {
		if _, err := store.Append(ctx, eventstore.AppendInput{VaultID: "vault-export", Type: eventTypes[index], SchemaVersion: 1, Sensitivity: event.SensitivityPrivate, Payload: marker, OccurredAt: now.Add(time.Duration(index+2) * time.Second), IdempotencyKey: fmt.Sprintf("export-event-%d", index)}); err != nil {
			t.Fatal(err)
		}
	}
	prepared, replay, err := store.PrepareSyncExport(ctx, syncstore.PrepareExportInput{VaultID: "vault-export", DeviceID: "device-local", Limit: 3, OccurredAt: now.Add(5 * time.Second)})
	if err != nil || replay || prepared.Batch.State != syncstate.ExportPreparing || prepared.Batch.SequenceStart != 1 || prepared.Batch.SequenceEnd != 2 || len(prepared.Events) != 2 {
		t.Fatalf("prepared=%+v replay=%v error=%v", prepared, replay, err)
	}
	replayed, replay, err := store.PrepareSyncExport(ctx, syncstore.PrepareExportInput{VaultID: "vault-export", DeviceID: "device-local", Limit: 1, OccurredAt: now.Add(6 * time.Second)})
	if err != nil || !replay || replayed.Batch.ExportID != prepared.Batch.ExportID || len(replayed.Events) != 2 {
		t.Fatalf("replayed=%+v replay=%v error=%v", replayed, replay, err)
	}
	encoded := []byte(`{"schema":"talos.sync-pack/1","private_marker":"ready-pack"}`)
	finalize := syncstore.FinalizeExportInput{ExportID: prepared.Batch.ExportID, VaultID: "vault-export", DeviceID: "device-local", PackID: "sha256:" + strings.Repeat("a", 64), SequenceStart: 1, SequenceEnd: 2, EventCount: 2, CiphertextHash: strings.Repeat("b", 64), EncodedPack: encoded, OccurredAt: now.Add(7 * time.Second)}
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
	if _, err := reopened.Append(ctx, eventstore.AppendInput{VaultID: "vault-export", Type: memoryStateChangedEventType, SchemaVersion: 1, Sensitivity: event.SensitivityPrivate, Payload: []byte(`{"item":"third"}`), OccurredAt: now.Add(9 * time.Second), IdempotencyKey: "export-event-3"}); err != nil {
		t.Fatal(err)
	}
	next, replay, err := reopened.PrepareSyncExport(ctx, syncstore.PrepareExportInput{VaultID: "vault-export", DeviceID: "device-local", Limit: 2, OccurredAt: now.Add(10 * time.Second)})
	if err != nil || replay || next.Batch.SequenceStart != 3 || next.Batch.SequenceEnd != 3 || len(next.Events) != 1 {
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
	if version != 18 {
		t.Fatalf("schema version=%d", version)
	}
	for _, table := range []string{"sync_devices", "sync_pack_receipts", "sync_event_origins", "sync_export_heads", "sync_export_batches", "sync_export_batch_events", "sync_replay_items", "sync_replay_batches"} {
		var name string
		if err := store.db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name); err != nil || name != table {
			t.Fatalf("table %s name=%q error=%v", table, name, err)
		}
	}
}

func TestValidatedSyncReplayAppliesCanonicalStateAndQuarantinesUnsafeEvents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 15, 0, 0, 0, time.UTC)
	vaultID := "vault-replay"
	baseline := strings.Repeat("a", 40)

	source := openTestStore(t, filepath.Join(t.TempDir(), "source.db"))
	defer source.Close()
	if _, err := source.CreateVault(ctx, vaultstore.CreateInput{VaultID: vaultID, RetentionDays: 30, OccurredAt: now, IdempotencyKey: "source-vault"}); err != nil {
		t.Fatal(err)
	}
	created, err := source.CreateTaskContract(ctx, taskstore.CreateInput{VaultID: vaultID, WorkspaceRoot: filepath.Join(t.TempDir(), "repository"), BaselineCommit: baseline, Goal: "create replay", AllowedPaths: []string{"internal/**"}, ForbiddenActions: []string{"git.push"}, AcceptanceCriteria: []string{"replay applies"}, Risk: task.RiskMedium, OccurredAt: now.Add(time.Second), IdempotencyKey: "source-task"})
	if err != nil {
		t.Fatal(err)
	}
	revised, err := source.ReviseTaskContract(ctx, taskstore.ReviseInput{VaultID: vaultID, TaskID: created.Task.ID, ExpectedRevision: 1, Goal: "create canonical replay", AllowedPaths: []string{"internal/**", "docs/**"}, ForbiddenActions: []string{"git.push"}, AcceptanceCriteria: []string{"replay applies", "conflicts quarantine"}, Risk: task.RiskHigh, OccurredAt: now.Add(2 * time.Second), IdempotencyKey: "source-task-revise"})
	if err != nil {
		t.Fatal(err)
	}
	question, err := source.CreateDecision(ctx, decisionstore.CreateInput{VaultID: vaultID, TaskID: created.Task.ID, Category: decision.CategoryBlocking, ExpectedRepositoryRevision: baseline, Question: "Apply remote state?", Reason: "The replay changes durable pointers.", RiskIfUnanswered: "The target remains stale.", SafeDefault: decision.SafeDefault{Action: "keep_local", ContinuableScopes: []string{"tests"}}, BlockingScopes: []string{"sync.apply"}, Options: []decision.Option{{ID: "apply", Label: "Apply", Consequence: "Use the verified pack."}, {ID: "hold", Label: "Hold", Consequence: "Leave the pack validated only."}}, OccurredAt: now.Add(3 * time.Second), IdempotencyKey: "source-decision"})
	if err != nil {
		t.Fatal(err)
	}
	answered, err := source.AnswerDecision(ctx, decisionstore.AnswerInput{VaultID: vaultID, DecisionID: question.Decision.ID, QuestionRevision: 1, ExpectedRepositoryRevision: baseline, SelectedOptionID: "apply", OccurredAt: now.Add(4 * time.Second), IdempotencyKey: "source-answer"})
	if err != nil || answered.Answer == nil {
		t.Fatalf("answered=%+v error=%v", answered, err)
	}
	candidate, err := source.CreateMemoryCandidate(ctx, memorystore.CreateCandidateInput{VaultID: vaultID, Kind: memory.KindDecision, Scope: memory.Scope{Kind: memory.ScopeVault}, Statement: "Verified sync packs may update canonical state.", Rationale: "The user selected apply after reviewing the replay boundary.", Applicability: memory.Applicability{GoalTerms: []string{"sync", "replay"}}, EvidenceEventIDs: []string{answered.Answer.EventID}, SourceActor: "user", Confidence: 90, Sensitivity: event.SensitivityPrivate, OccurredAt: now.Add(5 * time.Second), IdempotencyKey: "source-memory"})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := source.TransitionMemory(ctx, memorystore.TransitionInput{VaultID: vaultID, MemoryID: candidate.ID, ExpectedRevision: 1, NextState: memory.StateApproved, Reason: "Reviewed for replay", OccurredAt: now.Add(6 * time.Second), IdempotencyKey: "source-memory-approve"})
	if err != nil {
		t.Fatal(err)
	}

	eventIDs := []string{created.Contract.EventID, revised.Contract.EventID, question.Question.EventID, answered.Answer.EventID, candidate.LastEventID, approved.LastEventID}
	replayEvents := make([]syncstore.ReplayEvent, 0, len(eventIDs)+2)
	for index, eventID := range eventIDs {
		record, err := source.Get(ctx, eventID)
		if err != nil {
			t.Fatal(err)
		}
		replayEvents = append(replayEvents, syncstore.ReplayEvent{DeviceSeq: uint64(index + 1), Record: record})
	}
	createdRecord, err := source.Get(ctx, created.Contract.EventID)
	if err != nil {
		t.Fatal(err)
	}
	var conflictingPayload taskContractPayload
	if err := json.Unmarshal(createdRecord.Payload, &conflictingPayload); err != nil {
		t.Fatal(err)
	}
	conflictingPayload.Goal = "competing task creation"
	conflictingBytes, err := json.Marshal(conflictingPayload)
	if err != nil {
		t.Fatal(err)
	}
	replayEvents = append(replayEvents,
		syncstore.ReplayEvent{DeviceSeq: 7, Record: event.Record{ID: "remote-task-conflict", VaultID: vaultID, Type: taskContractCreatedEventType, SchemaVersion: taskContractEventSchemaVersion, Sensitivity: event.SensitivityPrivate, Payload: conflictingBytes, OccurredAt: createdRecord.OccurredAt}},
		syncstore.ReplayEvent{DeviceSeq: 8, Record: event.Record{ID: "remote-permission-event", VaultID: vaultID, Type: "permission.grant.created", SchemaVersion: 1, Sensitivity: event.SensitivityPrivate, Payload: []byte(`{"grant":"must-not-materialize"}`), OccurredAt: now.Add(7 * time.Second)}},
	)

	target := openTestStore(t, filepath.Join(t.TempDir(), "target.db"))
	defer target.Close()
	if _, err := target.CreateVault(ctx, vaultstore.CreateInput{VaultID: vaultID, RetentionDays: 30, OccurredAt: now, IdempotencyKey: "target-vault"}); err != nil {
		t.Fatal(err)
	}
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.RegisterSyncDevice(ctx, syncstore.RegisterDeviceInput{VaultID: vaultID, DeviceID: "source-device", PublicKey: publicKey, OccurredAt: now.Add(8 * time.Second), IdempotencyKey: "target-register-source"}); err != nil {
		t.Fatal(err)
	}
	packID := "sha256:" + strings.Repeat("c", 64)
	if _, _, err := target.RecordValidatedSyncPack(ctx, syncstore.RecordPackInput{PackID: packID, VaultID: vaultID, DeviceID: "source-device", SequenceStart: 1, SequenceEnd: 8, EventCount: 8, CiphertextHash: strings.Repeat("d", 64), EncodedPack: []byte("encrypted-pack-fixture"), ReceivedAt: now.Add(9 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	input := syncstore.ApplyReplayInput{PackID: packID, VaultID: vaultID, DeviceID: "source-device", Events: replayEvents, CompletedAt: now.Add(10 * time.Second)}
	result, replayed, err := target.ApplyValidatedSyncPack(ctx, input)
	if err != nil || replayed || result.Batch.AppliedCount != 6 || result.Batch.ConflictedCount != 1 || result.Batch.QuarantinedCount != 1 {
		t.Fatalf("result=%+v replayed=%v error=%v", result, replayed, err)
	}
	if result.Items[6].State != syncstate.ReplayConflicted || result.Items[6].ReasonCode != "aggregate_exists" || result.Items[7].State != syncstate.ReplayQuarantined || result.Items[7].ReasonCode != "event_type_not_syncable" {
		t.Fatalf("unexpected terminal replay items: %+v", result.Items[6:])
	}
	replayedResult, replayed, err := target.ApplyValidatedSyncPack(ctx, input)
	if err != nil || !replayed || replayedResult.Batch.LastEventID != result.Batch.LastEventID {
		t.Fatalf("replayedResult=%+v replayed=%v error=%v", replayedResult, replayed, err)
	}
	changedInput := input
	changedInput.Events = append([]syncstore.ReplayEvent(nil), input.Events...)
	changedInput.Events[0].Record.Payload = []byte(`{"changed":true}`)
	if _, _, err := target.ApplyValidatedSyncPack(ctx, changedInput); !errors.Is(err, syncstore.ErrReplayConflict) {
		t.Fatalf("changed replay input error=%v", err)
	}
	storedReplay, err := target.GetSyncReplay(ctx, vaultID, packID)
	if err != nil || storedReplay.Batch != result.Batch || len(storedReplay.Items) != len(result.Items) {
		t.Fatalf("storedReplay=%+v error=%v", storedReplay, err)
	}
	targetTask, err := target.GetTask(ctx, created.Task.ID)
	if err != nil || targetTask.CurrentRevision != 2 || targetTask.LastEventID != revised.Contract.EventID {
		t.Fatalf("targetTask=%+v error=%v", targetTask, err)
	}
	targetDecision, err := target.GetDecision(ctx, question.Decision.ID)
	if err != nil || targetDecision.Decision.State != decision.StateAnswered || targetDecision.Answer == nil || targetDecision.Answer.ID != answered.Answer.ID {
		t.Fatalf("targetDecision=%+v error=%v", targetDecision, err)
	}
	targetMemory, err := target.GetMemory(ctx, vaultID, candidate.ID)
	if err != nil || targetMemory.State != memory.StateApproved || targetMemory.Revision != 2 || targetMemory.LastEventID != approved.LastEventID {
		t.Fatalf("targetMemory=%+v error=%v", targetMemory, err)
	}
	if _, _, err := target.PrepareSyncExport(ctx, syncstore.PrepareExportInput{VaultID: vaultID, DeviceID: "source-device", Limit: 32, OccurredAt: now.Add(11 * time.Second)}); !errors.Is(err, syncstore.ErrNoExportableEvents) {
		t.Fatalf("imported events were selected for re-export: %v", err)
	}
}
