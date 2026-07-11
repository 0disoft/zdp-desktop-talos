package sqliteevent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const currentSchemaVersion = 3

var ErrUnsupportedSchema = errors.New("sqlite event store schema is newer than this application")

type migration struct {
	version    int
	statements []string
}

var migrations = []migration{
	{
		version: 1,
		statements: []string{
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
		},
	},
	{
		version: 2,
		statements: []string{
			`ALTER TABLE idempotency_keys ADD COLUMN request_hash BLOB`,
		},
	},
	{
		version: 3,
		statements: []string{
			`CREATE TABLE vault_states (
				vault_id TEXT PRIMARY KEY,
				revision INTEGER NOT NULL CHECK (revision > 0),
				status TEXT NOT NULL CHECK (status IN ('active','purged')),
				retention_days INTEGER NOT NULL CHECK (retention_days BETWEEN 1 AND 3650),
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				last_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT
			) STRICT`,
		},
	},
}

func applyMigrations(ctx context.Context, db *sql.DB) error {
	version, err := schemaVersion(ctx, db)
	if err != nil {
		return err
	}
	if version > currentSchemaVersion {
		return fmt.Errorf("%w: database=%d application=%d", ErrUnsupportedSchema, version, currentSchemaVersion)
	}
	for _, candidate := range migrations {
		if candidate.version <= version {
			continue
		}
		if candidate.version != version+1 {
			return fmt.Errorf("sqlite event store migration sequence has a gap after version %d", version)
		}
		if err := applyMigration(ctx, db, candidate); err != nil {
			return err
		}
		version = candidate.version
	}
	if version != currentSchemaVersion {
		return fmt.Errorf("sqlite event store migration stopped at version %d, expected %d", version, currentSchemaVersion)
	}
	return validateSchema(ctx, db)
}

func validateSchema(ctx context.Context, db *sql.DB) error {
	queries := []string{
		`SELECT event_id, vault_id, event_type, schema_version, sensitivity, payload_envelope, occurred_at FROM events LIMIT 0`,
		`SELECT idempotency_key, event_id, request_hash FROM idempotency_keys LIMIT 0`,
		`SELECT vault_id, revision, status, retention_days, created_at, updated_at, last_event_id FROM vault_states LIMIT 0`,
	}
	for _, query := range queries {
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return fmt.Errorf("validate sqlite event store schema: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close sqlite event store schema validation query: %w", err)
		}
	}
	return nil
}

func schemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("read sqlite event store schema version: %w", err)
	}
	return version, nil
}

func applyMigration(ctx context.Context, db *sql.DB, candidate migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sqlite event store migration %d: %w", candidate.version, err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, statement := range candidate.statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply sqlite event store migration %d: %w", candidate.version, err)
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", candidate.version)); err != nil {
		return fmt.Errorf("record sqlite event store migration %d: %w", candidate.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite event store migration %d: %w", candidate.version, err)
	}
	return nil
}
