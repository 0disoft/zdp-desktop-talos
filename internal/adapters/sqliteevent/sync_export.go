package sqliteevent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/syncstate"
	"github.com/0disoft/zdp-desktop-talos/internal/id"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/syncstore"
	"github.com/0disoft/zdp-desktop-talos/internal/security/envelope"
)

func (s *Store) PrepareSyncExport(ctx context.Context, input syncstore.PrepareExportInput) (syncstore.PreparedExport, bool, error) {
	if ctx == nil || input.VaultID == "" || input.DeviceID == "" || len(input.DeviceID) > syncstate.MaxDeviceIDLength || input.Limit < 1 || input.Limit > syncstate.MaxEventsPerPack {
		return syncstore.PreparedExport{}, false, syncstore.ErrInvalidCommand
	}
	occurredAt := normalizedTime(input.OccurredAt, s.now)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return syncstore.PreparedExport{}, false, fmt.Errorf("begin sync export reservation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	device, exists, err := getSyncDevicePointer(ctx, tx, input.VaultID, input.DeviceID)
	if err != nil {
		return syncstore.PreparedExport{}, false, err
	}
	if !exists {
		return syncstore.PreparedExport{}, false, syncstore.ErrDeviceNotFound
	}
	if device.State != syncstate.DeviceActive {
		return syncstore.PreparedExport{}, false, syncstore.ErrDeviceRevoked
	}
	if occurredAt.Before(device.UpdatedAt) {
		return syncstore.PreparedExport{}, false, syncstore.ErrInvalidCommand
	}

	existingID, found, err := findPreparingExportID(ctx, tx, input.VaultID, input.DeviceID)
	if err != nil {
		return syncstore.PreparedExport{}, false, err
	}
	if found {
		prepared, err := s.loadPreparedExport(ctx, tx, input.VaultID, existingID)
		return prepared, true, err
	}

	stamp := occurredAt.Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO sync_export_heads(vault_id, device_id, next_sequence, updated_at) VALUES(?, ?, 1, ?)`, input.VaultID, input.DeviceID, stamp); err != nil {
		return syncstore.PreparedExport{}, false, fmt.Errorf("initialize sync export head: %w", err)
	}
	var sequenceStart int64
	if err := tx.QueryRowContext(ctx, `SELECT next_sequence FROM sync_export_heads WHERE vault_id = ? AND device_id = ?`, input.VaultID, input.DeviceID).Scan(&sequenceStart); err != nil || sequenceStart < 1 {
		if err == nil {
			err = syncstore.ErrExportConflict
		}
		return syncstore.PreparedExport{}, false, err
	}

	records, err := s.selectUnassignedExportEvents(ctx, tx, input.VaultID, input.Limit)
	if err != nil {
		return syncstore.PreparedExport{}, false, err
	}
	if len(records) == 0 {
		return syncstore.PreparedExport{}, false, syncstore.ErrNoExportableEvents
	}
	exportID, err := id.UUIDv7(occurredAt, s.random)
	if err != nil {
		return syncstore.PreparedExport{}, false, fmt.Errorf("generate sync export id: %w", err)
	}
	sequenceEnd := sequenceStart + int64(len(records)) - 1
	batch := syncstate.ExportBatch{ExportID: exportID, VaultID: input.VaultID, DeviceID: input.DeviceID, SequenceStart: uint64(sequenceStart), SequenceEnd: uint64(sequenceEnd), EventCount: len(records), State: syncstate.ExportPreparing, CreatedAt: occurredAt, UpdatedAt: occurredAt}
	if batch.Validate() != nil {
		return syncstore.PreparedExport{}, false, syncstore.ErrInvalidCommand
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_export_batches(export_id, vault_id, device_id, sequence_start, sequence_end, event_count, state, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, 'preparing', ?, ?)`, batch.ExportID, batch.VaultID, batch.DeviceID, sequenceStart, sequenceEnd, batch.EventCount, stamp, stamp); err != nil {
		return syncstore.PreparedExport{}, false, fmt.Errorf("insert sync export batch: %w", err)
	}
	for index, record := range records {
		deviceSequence := sequenceStart + int64(index)
		if _, err := tx.ExecContext(ctx, `INSERT INTO sync_event_origins(event_id, vault_id, device_id, device_seq, origin_kind) VALUES(?, ?, ?, ?, 'local')`, record.ID, input.VaultID, input.DeviceID, deviceSequence); err != nil {
			return syncstore.PreparedExport{}, false, fmt.Errorf("assign sync event origin: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO sync_export_batch_events(export_id, event_id, device_seq) VALUES(?, ?, ?)`, batch.ExportID, record.ID, deviceSequence); err != nil {
			return syncstore.PreparedExport{}, false, fmt.Errorf("append sync export event: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE sync_export_heads SET next_sequence = ?, updated_at = ? WHERE vault_id = ? AND device_id = ? AND next_sequence = ?`, sequenceEnd+1, stamp, input.VaultID, input.DeviceID, sequenceStart)
	if err != nil {
		return syncstore.PreparedExport{}, false, err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return syncstore.PreparedExport{}, false, syncstore.ErrExportConflict
	}
	if err := tx.Commit(); err != nil {
		return syncstore.PreparedExport{}, false, fmt.Errorf("commit sync export reservation: %w", err)
	}
	return syncstore.PreparedExport{Batch: batch, Events: records}, false, nil
}

func (s *Store) FinalizeSyncExport(ctx context.Context, input syncstore.FinalizeExportInput) (syncstate.ExportBatch, []byte, bool, error) {
	if ctx == nil || input.ExportID == "" || input.VaultID == "" || input.DeviceID == "" || len(input.EncodedPack) == 0 || len(input.EncodedPack) > syncstate.MaxPackBytes || input.OccurredAt.IsZero() {
		return syncstate.ExportBatch{}, nil, false, syncstore.ErrInvalidCommand
	}
	input.OccurredAt = input.OccurredAt.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return syncstate.ExportBatch{}, nil, false, fmt.Errorf("begin sync export finalization: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	batch, encoded, found, err := s.getSyncExport(ctx, tx, input.VaultID, input.ExportID)
	if err != nil {
		return syncstate.ExportBatch{}, nil, false, err
	}
	if !found || batch.DeviceID != input.DeviceID {
		return syncstate.ExportBatch{}, nil, false, syncstore.ErrExportConflict
	}
	if batch.State == syncstate.ExportReady {
		return batch, encoded, true, nil
	}
	if batch.SequenceStart != input.SequenceStart || batch.SequenceEnd != input.SequenceEnd || batch.EventCount != input.EventCount {
		return syncstate.ExportBatch{}, nil, false, syncstore.ErrExportConflict
	}
	ready := batch
	ready.State = syncstate.ExportReady
	ready.PackID = input.PackID
	ready.CiphertextHash = input.CiphertextHash
	ready.UpdatedAt = input.OccurredAt
	if ready.Validate() != nil || ready.UpdatedAt.Before(ready.CreatedAt) {
		return syncstate.ExportBatch{}, nil, false, syncstore.ErrInvalidCommand
	}
	protected, err := s.sealer.Seal(input.EncodedPack, syncExportAAD(input.VaultID, input.PackID))
	if err != nil {
		return syncstate.ExportBatch{}, nil, false, fmt.Errorf("encrypt ready sync export: %w", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE sync_export_batches SET state = 'ready', pack_id = ?, ciphertext_hash = ?, pack_envelope = ?, updated_at = ? WHERE export_id = ? AND vault_id = ? AND device_id = ? AND state = 'preparing'`, ready.PackID, ready.CiphertextHash, protected, ready.UpdatedAt.Format(time.RFC3339Nano), ready.ExportID, ready.VaultID, ready.DeviceID)
	if err != nil {
		return syncstate.ExportBatch{}, nil, false, err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return syncstate.ExportBatch{}, nil, false, syncstore.ErrExportConflict
	}
	if err := tx.Commit(); err != nil {
		return syncstate.ExportBatch{}, nil, false, fmt.Errorf("commit sync export finalization: %w", err)
	}
	return ready, append([]byte(nil), input.EncodedPack...), false, nil
}

func (s *Store) GetSyncExport(ctx context.Context, vaultID, exportID string) (syncstate.ExportBatch, []byte, error) {
	batch, encoded, found, err := s.getSyncExport(ctx, s.db, vaultID, exportID)
	if err != nil {
		return syncstate.ExportBatch{}, nil, err
	}
	if !found || batch.State != syncstate.ExportReady {
		return syncstate.ExportBatch{}, nil, syncstore.ErrExportConflict
	}
	return batch, encoded, nil
}

func findPreparingExportID(ctx context.Context, queryer syncQueryer, vaultID, deviceID string) (string, bool, error) {
	var exportID string
	err := queryer.QueryRowContext(ctx, `SELECT export_id FROM sync_export_batches WHERE vault_id = ? AND device_id = ? AND state = 'preparing'`, vaultID, deviceID).Scan(&exportID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return exportID, err == nil, err
}

func (s *Store) loadPreparedExport(ctx context.Context, queryer syncReadQueryer, vaultID, exportID string) (syncstore.PreparedExport, error) {
	batch, _, found, err := s.getSyncExport(ctx, queryer, vaultID, exportID)
	if err != nil {
		return syncstore.PreparedExport{}, err
	}
	if !found || batch.State != syncstate.ExportPreparing {
		return syncstore.PreparedExport{}, syncstore.ErrExportConflict
	}
	events, err := s.loadExportEvents(ctx, queryer, batch.ExportID)
	if err != nil || len(events) != batch.EventCount {
		if err == nil {
			err = syncstore.ErrExportConflict
		}
		return syncstore.PreparedExport{}, err
	}
	return syncstore.PreparedExport{Batch: batch, Events: events}, nil
}

func (s *Store) selectUnassignedExportEvents(ctx context.Context, tx *sql.Tx, vaultID string, limit int) ([]event.Record, error) {
	rows, err := tx.QueryContext(ctx, `SELECT e.event_id, e.vault_id, e.event_type, e.schema_version, e.sensitivity, e.payload_envelope, e.occurred_at
		FROM events e
		WHERE e.vault_id = ? AND e.event_type NOT LIKE 'sync.%'
		AND NOT EXISTS (SELECT 1 FROM sync_event_origins o WHERE o.event_id = e.event_id)
		ORDER BY e.occurred_at, e.event_id LIMIT ?`, vaultID, limit)
	if err != nil {
		return nil, fmt.Errorf("select sync export events: %w", err)
	}
	defer rows.Close()
	records := make([]event.Record, 0, limit)
	for rows.Next() {
		record, err := s.scanRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

type syncReadQueryer interface {
	syncQueryer
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (s *Store) loadExportEvents(ctx context.Context, queryer syncReadQueryer, exportID string) ([]event.Record, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT e.event_id, e.vault_id, e.event_type, e.schema_version, e.sensitivity, e.payload_envelope, e.occurred_at
		FROM sync_export_batch_events b JOIN events e ON e.event_id = b.event_id
		WHERE b.export_id = ? ORDER BY b.device_seq`, exportID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []event.Record
	for rows.Next() {
		record, err := s.scanRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) getSyncExport(ctx context.Context, queryer syncQueryer, vaultID, exportID string) (syncstate.ExportBatch, []byte, bool, error) {
	var batch syncstate.ExportBatch
	var sequenceStart, sequenceEnd int64
	var state, createdAt, updatedAt string
	var protected []byte
	err := queryer.QueryRowContext(ctx, `SELECT export_id, vault_id, device_id, sequence_start, sequence_end, event_count, state, pack_id, ciphertext_hash, pack_envelope, created_at, updated_at FROM sync_export_batches WHERE vault_id = ? AND export_id = ?`, vaultID, exportID).Scan(&batch.ExportID, &batch.VaultID, &batch.DeviceID, &sequenceStart, &sequenceEnd, &batch.EventCount, &state, &batch.PackID, &batch.CiphertextHash, &protected, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return syncstate.ExportBatch{}, nil, false, nil
	}
	if err != nil {
		return syncstate.ExportBatch{}, nil, false, err
	}
	batch.SequenceStart = uint64(sequenceStart)
	batch.SequenceEnd = uint64(sequenceEnd)
	batch.State = syncstate.ExportState(state)
	batch.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	batch.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	if batch.Validate() != nil {
		return syncstate.ExportBatch{}, nil, false, syncstore.ErrExportConflict
	}
	if batch.State == syncstate.ExportPreparing {
		return batch, nil, true, nil
	}
	encoded, err := s.sealer.Open(protected, syncExportAAD(batch.VaultID, batch.PackID))
	if err != nil {
		return syncstate.ExportBatch{}, nil, false, fmt.Errorf("decrypt ready sync export: %w", err)
	}
	return batch, encoded, true, nil
}

func syncExportAAD(vaultID, packID string) envelope.AAD {
	return envelope.AAD{VaultID: vaultID, ObjectID: "sync-export-pack:" + packID, SchemaVersion: 1, Sensitivity: "sensitive"}
}
