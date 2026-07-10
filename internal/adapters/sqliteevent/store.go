package sqliteevent

import (
	"context"
	"crypto/rand"
	"database/sql"
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
	ErrNotFound           = errors.New("event not found")
	ErrInvalidAppendInput = errors.New("invalid append input")
)

type Store struct {
	db     *sql.DB
	sealer *envelope.Sealer
	now    func() time.Time
	random io.Reader
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

	store := &Store{db: db, sealer: sealer, now: func() time.Time { return time.Now().UTC() }, random: rand.Reader}
	if err := store.initialize(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) initialize(ctx context.Context) error {
	statements := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=FULL",
		"PRAGMA foreign_keys=ON",
		`CREATE TABLE IF NOT EXISTS events (
			event_id TEXT PRIMARY KEY,
			vault_id TEXT NOT NULL,
			event_type TEXT NOT NULL,
			schema_version INTEGER NOT NULL CHECK (schema_version > 0),
			sensitivity TEXT NOT NULL CHECK (sensitivity IN ('public','private','sensitive','secret')),
			payload_envelope BLOB NOT NULL,
			occurred_at TEXT NOT NULL
		) STRICT`,
		`CREATE TABLE IF NOT EXISTS idempotency_keys (
			idempotency_key TEXT PRIMARY KEY,
			event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT
		) STRICT`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize sqlite event store: %w", err)
		}
	}
	return nil
}

func (s *Store) Append(ctx context.Context, input eventstore.AppendInput) (event.Record, error) {
	if input.VaultID == "" || input.Type == "" || input.SchemaVersion < 1 || input.IdempotencyKey == "" {
		return event.Record{}, ErrInvalidAppendInput
	}
	if input.OccurredAt.IsZero() {
		input.OccurredAt = s.now()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return event.Record{}, fmt.Errorf("begin append transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := s.getByIdempotency(ctx, tx, input.IdempotencyKey)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return event.Record{}, err
	}

	eventID, err := id.UUIDv7(input.OccurredAt, s.random)
	if err != nil {
		return event.Record{}, fmt.Errorf("generate event id: %w", err)
	}
	record := event.Record{
		ID:            eventID,
		VaultID:       input.VaultID,
		Type:          input.Type,
		SchemaVersion: input.SchemaVersion,
		Sensitivity:   input.Sensitivity,
		Payload:       append([]byte(nil), input.Payload...),
		OccurredAt:    input.OccurredAt.UTC(),
	}
	if err := record.Validate(); err != nil {
		return event.Record{}, err
	}

	encrypted, err := s.sealer.Seal(record.Payload, associatedData(record))
	if err != nil {
		return event.Record{}, fmt.Errorf("encrypt event payload: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO events(event_id, vault_id, event_type, schema_version, sensitivity, payload_envelope, occurred_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.VaultID, record.Type, record.SchemaVersion, string(record.Sensitivity), encrypted, record.OccurredAt.Format(time.RFC3339Nano),
	); err != nil {
		return event.Record{}, fmt.Errorf("insert event: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO idempotency_keys(idempotency_key, event_id) VALUES(?, ?)",
		input.IdempotencyKey, record.ID,
	); err != nil {
		return event.Record{}, fmt.Errorf("claim idempotency key: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return event.Record{}, fmt.Errorf("commit event append: %w", err)
	}
	return record, nil
}

func (s *Store) Get(ctx context.Context, eventID string) (event.Record, error) {
	row := s.db.QueryRowContext(ctx, `SELECT event_id, vault_id, event_type, schema_version, sensitivity, payload_envelope, occurred_at
		FROM events WHERE event_id = ?`, eventID)
	return s.scanRecord(row)
}

func (s *Store) getByIdempotency(ctx context.Context, tx *sql.Tx, key string) (event.Record, error) {
	row := tx.QueryRowContext(ctx, `SELECT e.event_id, e.vault_id, e.event_type, e.schema_version, e.sensitivity, e.payload_envelope, e.occurred_at
		FROM idempotency_keys i JOIN events e ON e.event_id = i.event_id WHERE i.idempotency_key = ?`, key)
	return s.scanRecord(row)
}

type scanner interface {
	Scan(...any) error
}

func (s *Store) scanRecord(row scanner) (event.Record, error) {
	var (
		record      event.Record
		sensitivity string
		encrypted   []byte
		occurredAt  string
	)
	if err := row.Scan(&record.ID, &record.VaultID, &record.Type, &record.SchemaVersion, &sensitivity, &encrypted, &occurredAt); err != nil {
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
