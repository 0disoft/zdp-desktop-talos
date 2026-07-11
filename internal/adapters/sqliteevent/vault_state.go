package sqliteevent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/vault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
)

const (
	vaultCreatedEventType          = "vault.created"
	vaultRetentionUpdatedEventType = "vault.retention.updated"
	vaultEventSchemaVersion        = 1
)

type vaultStatePayload struct {
	Revision      int    `json:"revision"`
	RetentionDays int    `json:"retention_days"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

func (s *Store) CreateVault(ctx context.Context, input vaultstore.CreateInput) (vault.Record, error) {
	if input.VaultID == "" || input.IdempotencyKey == "" {
		return vault.Record{}, vaultstore.ErrInvalidCommand
	}
	if err := vault.ValidateRetentionDays(input.RetentionDays); err != nil {
		return vault.Record{}, fmt.Errorf("%w: %v", vaultstore.ErrInvalidCommand, err)
	}
	requestHash, err := vaultCommandHash(vaultCreatedEventType, input.VaultID, 0, input.RetentionDays, input.OccurredAt)
	if err != nil {
		return vault.Record{}, fmt.Errorf("hash create vault command: %w", err)
	}
	occurredAt := input.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = s.now()
	}
	occurredAt = occurredAt.UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return vault.Record{}, fmt.Errorf("begin create vault transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	existingEvent, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, requestHash)
	if err != nil {
		return vault.Record{}, mapVaultIdempotencyError(err)
	}
	if found {
		return vaultRecordFromEvent(existingEvent, vaultCreatedEventType)
	}
	if _, err := scanVaultRecord(tx.QueryRowContext(ctx, vaultStateQuery+" WHERE vault_id = ?", input.VaultID)); err == nil {
		return vault.Record{}, vaultstore.ErrAlreadyExists
	} else if !errors.Is(err, vaultstore.ErrNotFound) {
		return vault.Record{}, err
	}

	payload := vaultStatePayload{
		Revision:      1,
		RetentionDays: input.RetentionDays,
		CreatedAt:     occurredAt.Format(time.RFC3339Nano),
		UpdatedAt:     occurredAt.Format(time.RFC3339Nano),
	}
	record, err := s.vaultEvent(input.VaultID, vaultCreatedEventType, payload, occurredAt)
	if err != nil {
		return vault.Record{}, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return vault.Record{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO vault_states(vault_id, revision, status, retention_days, created_at, updated_at, last_event_id)
		VALUES(?, ?, ?, ?, ?, ?, ?)`,
		input.VaultID, payload.Revision, string(vault.StatusActive), payload.RetentionDays, payload.CreatedAt, payload.UpdatedAt, record.ID,
	); err != nil {
		return vault.Record{}, fmt.Errorf("insert vault state: %w", err)
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, record.ID, requestHash); err != nil {
		return vault.Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return vault.Record{}, fmt.Errorf("commit create vault: %w", err)
	}
	return vaultRecordFromEvent(record, vaultCreatedEventType)
}

func (s *Store) GetVault(ctx context.Context, vaultID string) (vault.Record, error) {
	if vaultID == "" {
		return vault.Record{}, vaultstore.ErrInvalidCommand
	}
	return scanVaultRecord(s.db.QueryRowContext(ctx, vaultStateQuery+" WHERE vault_id = ?", vaultID))
}

func (s *Store) UpdateVaultRetention(ctx context.Context, input vaultstore.UpdateRetentionInput) (vault.Record, error) {
	if input.VaultID == "" || input.IdempotencyKey == "" || input.ExpectedRevision < 1 {
		return vault.Record{}, vaultstore.ErrInvalidCommand
	}
	if err := vault.ValidateRetentionDays(input.RetentionDays); err != nil {
		return vault.Record{}, fmt.Errorf("%w: %v", vaultstore.ErrInvalidCommand, err)
	}
	requestHash, err := vaultCommandHash(vaultRetentionUpdatedEventType, input.VaultID, input.ExpectedRevision, input.RetentionDays, input.OccurredAt)
	if err != nil {
		return vault.Record{}, fmt.Errorf("hash update vault retention command: %w", err)
	}
	occurredAt := input.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = s.now()
	}
	occurredAt = occurredAt.UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return vault.Record{}, fmt.Errorf("begin update vault retention transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	existingEvent, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, requestHash)
	if err != nil {
		return vault.Record{}, mapVaultIdempotencyError(err)
	}
	if found {
		return vaultRecordFromEvent(existingEvent, vaultRetentionUpdatedEventType)
	}
	current, err := scanVaultRecord(tx.QueryRowContext(ctx, vaultStateQuery+" WHERE vault_id = ?", input.VaultID))
	if err != nil {
		return vault.Record{}, err
	}
	if current.Status != vault.StatusActive {
		return vault.Record{}, vaultstore.ErrInvalidCommand
	}
	if current.Revision != input.ExpectedRevision {
		return vault.Record{}, vaultstore.ErrRevisionConflict
	}
	if current.RetentionDays == input.RetentionDays {
		return vault.Record{}, fmt.Errorf("%w: retention is unchanged", vaultstore.ErrInvalidCommand)
	}
	if occurredAt.Before(current.UpdatedAt) {
		return vault.Record{}, fmt.Errorf("%w: occurrence time precedes current state", vaultstore.ErrInvalidCommand)
	}

	payload := vaultStatePayload{
		Revision:      current.Revision + 1,
		RetentionDays: input.RetentionDays,
		CreatedAt:     current.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:     occurredAt.Format(time.RFC3339Nano),
	}
	record, err := s.vaultEvent(input.VaultID, vaultRetentionUpdatedEventType, payload, occurredAt)
	if err != nil {
		return vault.Record{}, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return vault.Record{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE vault_states
		SET revision = ?, retention_days = ?, updated_at = ?, last_event_id = ?
		WHERE vault_id = ? AND revision = ? AND status = ?`,
		payload.Revision, payload.RetentionDays, payload.UpdatedAt, record.ID,
		input.VaultID, input.ExpectedRevision, string(vault.StatusActive),
	)
	if err != nil {
		return vault.Record{}, fmt.Errorf("update vault state: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return vault.Record{}, fmt.Errorf("inspect vault state update: %w", err)
	}
	if updated != 1 {
		return vault.Record{}, vaultstore.ErrRevisionConflict
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, record.ID, requestHash); err != nil {
		return vault.Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return vault.Record{}, fmt.Errorf("commit update vault retention: %w", err)
	}
	return vaultRecordFromEvent(record, vaultRetentionUpdatedEventType)
}

const vaultStateQuery = `SELECT vault_id, revision, status, retention_days, created_at, updated_at, last_event_id FROM vault_states`

func scanVaultRecord(row scanner) (vault.Record, error) {
	var record vault.Record
	var status, createdAt, updatedAt string
	if err := row.Scan(&record.ID, &record.Revision, &status, &record.RetentionDays, &createdAt, &updatedAt, &record.LastEventID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return vault.Record{}, vaultstore.ErrNotFound
		}
		return vault.Record{}, fmt.Errorf("scan vault state: %w", err)
	}
	record.Status = vault.Status(status)
	var err error
	record.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return vault.Record{}, fmt.Errorf("parse vault created_at: %w", err)
	}
	record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return vault.Record{}, fmt.Errorf("parse vault updated_at: %w", err)
	}
	if err := record.Validate(); err != nil {
		return vault.Record{}, fmt.Errorf("validate stored vault state: %w", err)
	}
	return record, nil
}

func (s *Store) vaultEvent(vaultID, eventType string, payload vaultStatePayload, occurredAt time.Time) (event.Record, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return event.Record{}, fmt.Errorf("encode %s payload: %w", eventType, err)
	}
	record, err := s.newEventRecord(vaultID, eventType, vaultEventSchemaVersion, event.SensitivityPrivate, encoded, occurredAt)
	if err != nil {
		return event.Record{}, err
	}
	return record, nil
}

func vaultRecordFromEvent(record event.Record, expectedType string) (vault.Record, error) {
	if record.Type != expectedType {
		return vault.Record{}, vaultstore.ErrIdempotencyConflict
	}
	var payload vaultStatePayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return vault.Record{}, fmt.Errorf("decode %s payload: %w", expectedType, err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
	if err != nil {
		return vault.Record{}, fmt.Errorf("parse %s created_at: %w", expectedType, err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, payload.UpdatedAt)
	if err != nil {
		return vault.Record{}, fmt.Errorf("parse %s updated_at: %w", expectedType, err)
	}
	result := vault.Record{
		ID:            record.VaultID,
		Revision:      payload.Revision,
		Status:        vault.StatusActive,
		RetentionDays: payload.RetentionDays,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
		LastEventID:   record.ID,
	}
	if err := result.Validate(); err != nil {
		return vault.Record{}, fmt.Errorf("validate %s result: %w", expectedType, err)
	}
	return result, nil
}

func vaultCommandHash(commandType, vaultID string, expectedRevision, retentionDays int, occurredAt time.Time) ([]byte, error) {
	explicitOccurredAt := ""
	if !occurredAt.IsZero() {
		explicitOccurredAt = occurredAt.UTC().Format(time.RFC3339Nano)
	}
	return requestHash(struct {
		CommandType      string `json:"command_type"`
		VaultID          string `json:"vault_id"`
		ExpectedRevision int    `json:"expected_revision"`
		RetentionDays    int    `json:"retention_days"`
		OccurredAt       string `json:"occurred_at"`
	}{
		CommandType:      commandType,
		VaultID:          vaultID,
		ExpectedRevision: expectedRevision,
		RetentionDays:    retentionDays,
		OccurredAt:       explicitOccurredAt,
	})
}

func mapVaultIdempotencyError(err error) error {
	switch {
	case errors.Is(err, ErrIdempotencyConflict):
		return vaultstore.ErrIdempotencyConflict
	case errors.Is(err, ErrIdempotencyUnverifiable):
		return vaultstore.ErrIdempotencyUnverified
	default:
		return err
	}
}
