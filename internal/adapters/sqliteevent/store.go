package sqliteevent

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/id"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/eventstore"
	"github.com/0disoft/zdp-desktop-talos/internal/security/envelope"
	_ "modernc.org/sqlite"
)

var (
	ErrNotFound                = errors.New("event not found")
	ErrInvalidAppendInput      = errors.New("invalid append input")
	ErrIdempotencyConflict     = errors.New("idempotency key was already used for a different request")
	ErrIdempotencyUnverifiable = errors.New("legacy idempotency result cannot be verified against the request")
)

type Store struct {
	db       *sql.DB
	sealer   *envelope.Sealer
	blobRoot string
	now      func() time.Time
	random   io.Reader
}

func Open(path string, sealer *envelope.Sealer) (*Store, error) {
	if path == "" || sealer == nil {
		return nil, fmt.Errorf("%w: database path and sealer are required", ErrInvalidAppendInput)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite event store: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &Store{db: db, sealer: sealer, blobRoot: path + ".blobs", now: func() time.Time { return time.Now().UTC() }, random: rand.Reader}
	if err := store.initialize(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) initialize(ctx context.Context) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=FULL",
		"PRAGMA foreign_keys=ON",
	}
	for _, statement := range pragmas {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize sqlite event store: %w", err)
		}
	}
	if err := applyMigrations(ctx, s.db); err != nil {
		return err
	}
	if err := ensureOwnedDirectory(s.blobRoot); err != nil {
		return err
	}
	return s.ReconcileArtifacts(ctx)
}

func (s *Store) Append(ctx context.Context, input eventstore.AppendInput) (event.Record, error) {
	if err := validateAppendInput(input); err != nil {
		return event.Record{}, err
	}
	requestHash, err := appendRequestHash(input)
	if err != nil {
		return event.Record{}, fmt.Errorf("hash append request: %w", err)
	}
	if input.OccurredAt.IsZero() {
		input.OccurredAt = s.now()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return event.Record{}, fmt.Errorf("begin append transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, requestHash)
	if err != nil {
		return event.Record{}, err
	}
	if found {
		return existing, nil
	}

	record, err := s.newEventRecord(input.VaultID, input.Type, input.SchemaVersion, input.Sensitivity, input.Payload, input.OccurredAt)
	if err != nil {
		return event.Record{}, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return event.Record{}, err
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, record.ID, requestHash); err != nil {
		return event.Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return event.Record{}, fmt.Errorf("commit event append: %w", err)
	}
	return record, nil
}

func (s *Store) resolveIdempotency(ctx context.Context, tx *sql.Tx, key string, requestHash []byte) (event.Record, bool, error) {
	existing, existingHash, err := s.getByIdempotency(ctx, tx, key)
	if errors.Is(err, ErrNotFound) {
		return event.Record{}, false, nil
	}
	if err != nil {
		return event.Record{}, false, err
	}
	if len(existingHash) == 0 {
		return event.Record{}, false, ErrIdempotencyUnverifiable
	}
	if !bytes.Equal(existingHash, requestHash) {
		return event.Record{}, false, ErrIdempotencyConflict
	}
	return existing, true, nil
}

func (s *Store) newEventRecord(vaultID, eventType string, schemaVersion int, sensitivity event.Sensitivity, payload []byte, occurredAt time.Time) (event.Record, error) {
	eventID, err := id.UUIDv7(occurredAt, s.random)
	if err != nil {
		return event.Record{}, fmt.Errorf("generate event id: %w", err)
	}
	record := event.Record{
		ID:            eventID,
		VaultID:       vaultID,
		Type:          eventType,
		SchemaVersion: schemaVersion,
		Sensitivity:   sensitivity,
		Payload:       append([]byte(nil), payload...),
		OccurredAt:    occurredAt.UTC(),
	}
	if err := record.Validate(); err != nil {
		return event.Record{}, err
	}
	return record, nil
}

func (s *Store) insertEvent(ctx context.Context, tx *sql.Tx, record event.Record) error {
	encrypted, err := s.sealer.Seal(record.Payload, associatedData(record))
	if err != nil {
		return fmt.Errorf("encrypt event payload: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO events(event_id, vault_id, event_type, schema_version, sensitivity, payload_envelope, occurred_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.VaultID, record.Type, record.SchemaVersion, string(record.Sensitivity), encrypted, record.OccurredAt.Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

func claimIdempotency(ctx context.Context, tx *sql.Tx, key, eventID string, requestHash []byte) error {
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO idempotency_keys(idempotency_key, event_id, request_hash) VALUES(?, ?, ?)",
		key, eventID, requestHash,
	); err != nil {
		return fmt.Errorf("claim idempotency key: %w", err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, eventID string) (event.Record, error) {
	row := s.db.QueryRowContext(ctx, `SELECT event_id, vault_id, event_type, schema_version, sensitivity, payload_envelope, occurred_at
		FROM events WHERE event_id = ?`, eventID)
	return s.scanRecord(row)
}

func (s *Store) getByIdempotency(ctx context.Context, tx *sql.Tx, key string) (event.Record, []byte, error) {
	row := tx.QueryRowContext(ctx, `SELECT e.event_id, e.vault_id, e.event_type, e.schema_version, e.sensitivity, e.payload_envelope, e.occurred_at, i.request_hash
		FROM idempotency_keys i JOIN events e ON e.event_id = i.event_id WHERE i.idempotency_key = ?`, key)
	var requestHash []byte
	record, err := s.scanRecordWithTail(row, &requestHash)
	return record, requestHash, err
}

type scanner interface {
	Scan(...any) error
}

func (s *Store) scanRecord(row scanner) (event.Record, error) {
	return s.scanRecordWithTail(row)
}

func (s *Store) scanRecordWithTail(row scanner, tail ...any) (event.Record, error) {
	var (
		record      event.Record
		sensitivity string
		encrypted   []byte
		occurredAt  string
	)
	destinations := []any{&record.ID, &record.VaultID, &record.Type, &record.SchemaVersion, &sensitivity, &encrypted, &occurredAt}
	destinations = append(destinations, tail...)
	if err := row.Scan(destinations...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return event.Record{}, ErrNotFound
		}
		return event.Record{}, fmt.Errorf("scan event: %w", err)
	}
	record.Sensitivity = event.Sensitivity(sensitivity)
	parsedTime, err := time.Parse(time.RFC3339Nano, occurredAt)
	if err != nil {
		return event.Record{}, fmt.Errorf("parse occurred_at: %w", err)
	}
	record.OccurredAt = parsedTime
	payload, err := s.sealer.Open(encrypted, associatedData(record))
	if err != nil {
		return event.Record{}, fmt.Errorf("decrypt event payload: %w", err)
	}
	record.Payload = payload
	if err := record.Validate(); err != nil {
		return event.Record{}, fmt.Errorf("validate stored event: %w", err)
	}
	return record, nil
}

func validateAppendInput(input eventstore.AppendInput) error {
	if input.VaultID == "" || input.Type == "" || input.SchemaVersion < 1 || input.IdempotencyKey == "" {
		return ErrInvalidAppendInput
	}
	occurredAt := input.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Unix(1, 0).UTC()
	}
	record := event.Record{
		ID:            "validation-event",
		VaultID:       input.VaultID,
		Type:          input.Type,
		SchemaVersion: input.SchemaVersion,
		Sensitivity:   input.Sensitivity,
		Payload:       input.Payload,
		OccurredAt:    occurredAt,
	}
	return record.Validate()
}

func appendRequestHash(input eventstore.AppendInput) ([]byte, error) {
	occurredAt := ""
	if !input.OccurredAt.IsZero() {
		occurredAt = input.OccurredAt.UTC().Format(time.RFC3339Nano)
	}
	return requestHash(struct {
		VaultID       string            `json:"vault_id"`
		Type          string            `json:"type"`
		SchemaVersion int               `json:"schema_version"`
		Sensitivity   event.Sensitivity `json:"sensitivity"`
		Payload       []byte            `json:"payload"`
		OccurredAt    string            `json:"occurred_at"`
	}{
		VaultID:       input.VaultID,
		Type:          input.Type,
		SchemaVersion: input.SchemaVersion,
		Sensitivity:   input.Sensitivity,
		Payload:       input.Payload,
		OccurredAt:    occurredAt,
	})
}

func requestHash(intent any) ([]byte, error) {
	encoded, err := json.Marshal(intent)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(encoded)
	return sum[:], nil
}

func (s *Store) Checkpoint(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("checkpoint event store: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func associatedData(record event.Record) envelope.AAD {
	return envelope.AAD{
		VaultID:       record.VaultID,
		ObjectID:      record.ID,
		SchemaVersion: record.SchemaVersion,
		Sensitivity:   string(record.Sensitivity),
	}
}
