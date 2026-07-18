package sqliteevent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/syncstate"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/syncstore"
	"github.com/0disoft/zdp-desktop-talos/internal/security/envelope"
)

const syncStateEventSchemaVersion = 1

type syncDevicePayload struct {
	VaultID      string                `json:"vault_id"`
	DeviceID     string                `json:"device_id"`
	PublicKey    string                `json:"public_key"`
	State        syncstate.DeviceState `json:"state"`
	Revision     int                   `json:"revision"`
	NextSequence uint64                `json:"next_sequence"`
	CreatedAt    string                `json:"created_at"`
	UpdatedAt    string                `json:"updated_at"`
}

type syncPackPayload struct {
	PackID         string `json:"pack_id"`
	VaultID        string `json:"vault_id"`
	DeviceID       string `json:"device_id"`
	SequenceStart  uint64 `json:"sequence_start"`
	SequenceEnd    uint64 `json:"sequence_end"`
	EventCount     int    `json:"event_count"`
	CiphertextHash string `json:"ciphertext_hash"`
	State          string `json:"state"`
	ReceivedAt     string `json:"received_at"`
}

func (s *Store) RegisterSyncDevice(ctx context.Context, input syncstore.RegisterDeviceInput) (syncstate.Device, error) {
	if input.VaultID == "" || input.DeviceID == "" || len(input.DeviceID) > syncstate.MaxDeviceIDLength || len(input.PublicKey) != ed25519.PublicKeySize || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 {
		return syncstate.Device{}, syncstore.ErrInvalidCommand
	}
	input.PublicKey = append(ed25519.PublicKey(nil), input.PublicKey...)
	occurredAt := normalizedTime(input.OccurredAt, s.now)
	requestHash, err := syncCommandHash(input)
	if err != nil {
		return syncstate.Device{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return syncstate.Device{}, fmt.Errorf("begin sync device registration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existingEvent, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, requestHash)
	if err != nil {
		return syncstate.Device{}, mapSyncIdempotencyError(err)
	}
	if found {
		return syncDeviceFromEvent(existingEvent)
	}
	if err := requireActiveVault(ctx, tx, input.VaultID); err != nil {
		return syncstate.Device{}, err
	}
	if _, exists, err := getSyncDevicePointer(ctx, tx, input.VaultID, input.DeviceID); err != nil {
		return syncstate.Device{}, err
	} else if exists {
		return syncstate.Device{}, syncstore.ErrDeviceExists
	}
	payload := syncDevicePayload{VaultID: input.VaultID, DeviceID: input.DeviceID, PublicKey: base64.RawStdEncoding.EncodeToString(input.PublicKey), State: syncstate.DeviceActive, Revision: 1, NextSequence: 1, CreatedAt: occurredAt.Format(time.RFC3339Nano), UpdatedAt: occurredAt.Format(time.RFC3339Nano)}
	record, err := s.syncEvent("sync.device.registered", input.VaultID, payload, occurredAt)
	if err != nil {
		return syncstate.Device{}, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return syncstate.Device{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_devices(vault_id, device_id, public_key, state, revision, next_sequence, created_at, updated_at, last_event_id) VALUES(?, ?, ?, 'active', 1, 1, ?, ?, ?)`, input.VaultID, input.DeviceID, []byte(input.PublicKey), payload.CreatedAt, payload.UpdatedAt, record.ID); err != nil {
		return syncstate.Device{}, fmt.Errorf("insert sync device: %w", err)
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, record.ID, requestHash); err != nil {
		return syncstate.Device{}, err
	}
	if err := tx.Commit(); err != nil {
		return syncstate.Device{}, fmt.Errorf("commit sync device registration: %w", err)
	}
	return syncDeviceFromPayload(payload, record.ID)
}

func (s *Store) RevokeSyncDevice(ctx context.Context, input syncstore.RevokeDeviceInput) (syncstate.Device, error) {
	if input.VaultID == "" || input.DeviceID == "" || input.ExpectedRevision < 1 || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 {
		return syncstate.Device{}, syncstore.ErrInvalidCommand
	}
	occurredAt := normalizedTime(input.OccurredAt, s.now)
	requestHash, err := syncCommandHash(input)
	if err != nil {
		return syncstate.Device{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return syncstate.Device{}, fmt.Errorf("begin sync device revocation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existingEvent, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, requestHash)
	if err != nil {
		return syncstate.Device{}, mapSyncIdempotencyError(err)
	}
	if found {
		return syncDeviceFromEvent(existingEvent)
	}
	pointer, exists, err := getSyncDevicePointer(ctx, tx, input.VaultID, input.DeviceID)
	if err != nil {
		return syncstate.Device{}, err
	}
	if !exists {
		return syncstate.Device{}, syncstore.ErrDeviceNotFound
	}
	if pointer.State != syncstate.DeviceActive || pointer.Revision != input.ExpectedRevision {
		return syncstate.Device{}, syncstore.ErrRevisionConflict
	}
	payload := syncDevicePayload{VaultID: pointer.VaultID, DeviceID: pointer.DeviceID, PublicKey: base64.RawStdEncoding.EncodeToString(pointer.PublicKey), State: syncstate.DeviceRevoked, Revision: pointer.Revision + 1, NextSequence: pointer.NextSequence, CreatedAt: pointer.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: occurredAt.Format(time.RFC3339Nano)}
	record, err := s.syncEvent("sync.device.revoked", input.VaultID, payload, occurredAt)
	if err != nil {
		return syncstate.Device{}, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return syncstate.Device{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE sync_devices SET state = 'revoked', revision = ?, updated_at = ?, last_event_id = ? WHERE vault_id = ? AND device_id = ? AND state = 'active' AND revision = ?`, payload.Revision, payload.UpdatedAt, record.ID, input.VaultID, input.DeviceID, input.ExpectedRevision)
	if err != nil {
		return syncstate.Device{}, err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return syncstate.Device{}, syncstore.ErrRevisionConflict
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, record.ID, requestHash); err != nil {
		return syncstate.Device{}, err
	}
	if err := tx.Commit(); err != nil {
		return syncstate.Device{}, err
	}
	return syncDeviceFromPayload(payload, record.ID)
}

func (s *Store) GetSyncDevice(ctx context.Context, vaultID, deviceID string) (syncstate.Device, error) {
	pointer, exists, err := getSyncDevicePointer(ctx, s.db, vaultID, deviceID)
	if err != nil {
		return syncstate.Device{}, err
	}
	if !exists {
		return syncstate.Device{}, syncstore.ErrDeviceNotFound
	}
	return pointer, nil
}

func (s *Store) RecordValidatedSyncPack(ctx context.Context, input syncstore.RecordPackInput) (syncstate.PackReceipt, bool, error) {
	if input.ReceivedAt.IsZero() {
		input.ReceivedAt = s.now()
	}
	input.ReceivedAt = input.ReceivedAt.UTC()
	candidate := syncstate.PackReceipt{PackID: input.PackID, VaultID: input.VaultID, DeviceID: input.DeviceID, SequenceStart: input.SequenceStart, SequenceEnd: input.SequenceEnd, EventCount: input.EventCount, CiphertextHash: input.CiphertextHash, State: syncstate.PackValidated, ReceivedAt: input.ReceivedAt, LastEventID: "validation-pending"}
	if candidate.Validate() != nil || len(input.EncodedPack) == 0 || len(input.EncodedPack) > syncstate.MaxPackBytes {
		return syncstate.PackReceipt{}, false, syncstore.ErrInvalidCommand
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return syncstate.PackReceipt{}, false, fmt.Errorf("begin sync pack receipt: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stored, storedPack, exists, err := s.getValidatedSyncPack(ctx, tx, input.VaultID, input.PackID)
	if err != nil {
		return syncstate.PackReceipt{}, false, err
	}
	if exists {
		if samePackReceipt(stored, candidate) && bytes.Equal(storedPack, input.EncodedPack) {
			return stored, true, nil
		}
		return syncstate.PackReceipt{}, false, syncstore.ErrPackConflict
	}
	device, exists, err := getSyncDevicePointer(ctx, tx, input.VaultID, input.DeviceID)
	if err != nil {
		return syncstate.PackReceipt{}, false, err
	}
	if !exists {
		return syncstate.PackReceipt{}, false, syncstore.ErrDeviceNotFound
	}
	if device.State != syncstate.DeviceActive {
		return syncstate.PackReceipt{}, false, syncstore.ErrDeviceRevoked
	}
	if input.ReceivedAt.Before(device.UpdatedAt) {
		return syncstate.PackReceipt{}, false, syncstore.ErrInvalidCommand
	}
	if device.NextSequence != input.SequenceStart {
		return syncstate.PackReceipt{}, false, syncstore.ErrSequenceConflict
	}
	encryptedPack, err := s.sealer.Seal(input.EncodedPack, syncPackAAD(input.VaultID, input.PackID))
	if err != nil {
		return syncstate.PackReceipt{}, false, fmt.Errorf("encrypt validated sync pack: %w", err)
	}
	payload := syncPackPayload{PackID: input.PackID, VaultID: input.VaultID, DeviceID: input.DeviceID, SequenceStart: input.SequenceStart, SequenceEnd: input.SequenceEnd, EventCount: input.EventCount, CiphertextHash: input.CiphertextHash, State: string(syncstate.PackValidated), ReceivedAt: input.ReceivedAt.Format(time.RFC3339Nano)}
	record, err := s.syncEvent("sync.pack.validated", input.VaultID, payload, input.ReceivedAt)
	if err != nil {
		return syncstate.PackReceipt{}, false, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return syncstate.PackReceipt{}, false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE sync_devices SET next_sequence = ?, updated_at = ?, last_event_id = ? WHERE vault_id = ? AND device_id = ? AND state = 'active' AND next_sequence = ?`, input.SequenceEnd+1, payload.ReceivedAt, record.ID, input.VaultID, input.DeviceID, input.SequenceStart)
	if err != nil {
		return syncstate.PackReceipt{}, false, err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return syncstate.PackReceipt{}, false, syncstore.ErrSequenceConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_pack_receipts(pack_id, vault_id, device_id, sequence_start, sequence_end, event_count, ciphertext_hash, pack_envelope, state, received_at, event_id) VALUES(?, ?, ?, ?, ?, ?, ?, ?, 'validated', ?, ?)`, input.PackID, input.VaultID, input.DeviceID, input.SequenceStart, input.SequenceEnd, input.EventCount, input.CiphertextHash, encryptedPack, payload.ReceivedAt, record.ID); err != nil {
		return syncstate.PackReceipt{}, false, fmt.Errorf("insert sync pack receipt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return syncstate.PackReceipt{}, false, err
	}
	candidate.LastEventID = record.ID
	return candidate, false, nil
}

func (s *Store) GetValidatedSyncPack(ctx context.Context, vaultID, packID string) (syncstate.PackReceipt, []byte, error) {
	receipt, encoded, exists, err := s.getValidatedSyncPack(ctx, s.db, vaultID, packID)
	if err != nil {
		return syncstate.PackReceipt{}, nil, err
	}
	if !exists {
		return syncstate.PackReceipt{}, nil, syncstore.ErrPackConflict
	}
	return receipt, encoded, nil
}

type syncQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getSyncDevicePointer(ctx context.Context, queryer syncQueryer, vaultID, deviceID string) (syncstate.Device, bool, error) {
	var device syncstate.Device
	var publicKey []byte
	var state, createdAt, updatedAt string
	err := queryer.QueryRowContext(ctx, `SELECT vault_id, device_id, public_key, state, revision, next_sequence, created_at, updated_at, last_event_id FROM sync_devices WHERE vault_id = ? AND device_id = ?`, vaultID, deviceID).Scan(&device.VaultID, &device.DeviceID, &publicKey, &state, &device.Revision, &device.NextSequence, &createdAt, &updatedAt, &device.LastEventID)
	if errors.Is(err, sql.ErrNoRows) {
		return syncstate.Device{}, false, nil
	}
	if err != nil {
		return syncstate.Device{}, false, err
	}
	device.PublicKey = append(ed25519.PublicKey(nil), publicKey...)
	device.State = syncstate.DeviceState(state)
	device.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	device.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	if device.Validate() != nil {
		return syncstate.Device{}, false, syncstore.ErrInvalidCommand
	}
	return device, true, nil
}

func (s *Store) getValidatedSyncPack(ctx context.Context, queryer syncQueryer, vaultID, packID string) (syncstate.PackReceipt, []byte, bool, error) {
	var receipt syncstate.PackReceipt
	var sequenceStart, sequenceEnd int64
	var state, receivedAt string
	var encrypted []byte
	err := queryer.QueryRowContext(ctx, `SELECT pack_id, vault_id, device_id, sequence_start, sequence_end, event_count, ciphertext_hash, pack_envelope, state, received_at, event_id FROM sync_pack_receipts WHERE vault_id = ? AND pack_id = ?`, vaultID, packID).Scan(&receipt.PackID, &receipt.VaultID, &receipt.DeviceID, &sequenceStart, &sequenceEnd, &receipt.EventCount, &receipt.CiphertextHash, &encrypted, &state, &receivedAt, &receipt.LastEventID)
	if errors.Is(err, sql.ErrNoRows) {
		return syncstate.PackReceipt{}, nil, false, nil
	}
	if err != nil {
		return syncstate.PackReceipt{}, nil, false, err
	}
	receipt.SequenceStart = uint64(sequenceStart)
	receipt.SequenceEnd = uint64(sequenceEnd)
	receipt.State = syncstate.PackState(state)
	receipt.ReceivedAt, _ = time.Parse(time.RFC3339Nano, receivedAt)
	if receipt.Validate() != nil {
		return syncstate.PackReceipt{}, nil, false, syncstore.ErrInvalidCommand
	}
	encoded, err := s.sealer.Open(encrypted, syncPackAAD(receipt.VaultID, receipt.PackID))
	if err != nil {
		return syncstate.PackReceipt{}, nil, false, fmt.Errorf("decrypt validated sync pack: %w", err)
	}
	return receipt, encoded, true, nil
}

func (s *Store) syncEvent(eventType, vaultID string, payload any, occurredAt time.Time) (event.Record, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return event.Record{}, err
	}
	return s.newEventRecord(vaultID, eventType, syncStateEventSchemaVersion, event.SensitivityPrivate, encoded, occurredAt)
}

func syncDeviceFromEvent(record event.Record) (syncstate.Device, error) {
	if record.SchemaVersion != syncStateEventSchemaVersion || record.Sensitivity != event.SensitivityPrivate || record.Type != "sync.device.registered" && record.Type != "sync.device.revoked" {
		return syncstate.Device{}, syncstore.ErrInvalidCommand
	}
	var payload syncDevicePayload
	if json.Unmarshal(record.Payload, &payload) != nil || payload.VaultID != record.VaultID {
		return syncstate.Device{}, syncstore.ErrInvalidCommand
	}
	return syncDeviceFromPayload(payload, record.ID)
}

func syncDeviceFromPayload(payload syncDevicePayload, eventID string) (syncstate.Device, error) {
	publicKey, err := base64.RawStdEncoding.DecodeString(payload.PublicKey)
	if err != nil {
		return syncstate.Device{}, syncstore.ErrInvalidCommand
	}
	createdAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
	if err != nil {
		return syncstate.Device{}, syncstore.ErrInvalidCommand
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, payload.UpdatedAt)
	if err != nil {
		return syncstate.Device{}, syncstore.ErrInvalidCommand
	}
	device := syncstate.Device{VaultID: payload.VaultID, DeviceID: payload.DeviceID, PublicKey: ed25519.PublicKey(publicKey), State: payload.State, Revision: payload.Revision, NextSequence: payload.NextSequence, CreatedAt: createdAt, UpdatedAt: updatedAt, LastEventID: eventID}
	if device.Validate() != nil {
		return syncstate.Device{}, syncstore.ErrInvalidCommand
	}
	return device, nil
}

func syncCommandHash(input any) ([]byte, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	return hash[:], nil
}

func mapSyncIdempotencyError(err error) error {
	if errors.Is(err, ErrIdempotencyConflict) || errors.Is(err, ErrIdempotencyUnverifiable) {
		return syncstore.ErrIdempotencyConflict
	}
	return err
}

func samePackReceipt(left, right syncstate.PackReceipt) bool {
	return left.PackID == right.PackID && left.VaultID == right.VaultID && left.DeviceID == right.DeviceID && left.SequenceStart == right.SequenceStart && left.SequenceEnd == right.SequenceEnd && left.EventCount == right.EventCount && left.CiphertextHash == right.CiphertextHash
}

func syncPackAAD(vaultID, packID string) envelope.AAD {
	return envelope.AAD{VaultID: vaultID, ObjectID: "sync-pack-receipt:" + packID, SchemaVersion: 1, Sensitivity: "sensitive"}
}
