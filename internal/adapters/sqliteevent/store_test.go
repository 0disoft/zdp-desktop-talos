package sqliteevent

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/eventstore"
	"github.com/0disoft/zdp-desktop-talos/internal/security/envelope"
)

func TestAppendIsIdempotentAndSurvivesRestartWithoutPlaintext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "vault.db")
	key := bytes.Repeat([]byte{0x19}, 32)
	sealer, err := envelope.NewSealer("test-key", key)
	if err != nil {
		t.Fatalf("NewSealer returned error: %v", err)
	}
	store, err := Open(databasePath, sealer)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}

	marker := []byte("talos-private-marker-should-never-be-plaintext")
	input := eventstore.AppendInput{
		VaultID:        "vault-test",
		Type:           "task.created",
		SchemaVersion:  1,
		Sensitivity:    event.SensitivityPrivate,
		Payload:        marker,
		OccurredAt:     time.Unix(1_720_000_000, 0).UTC(),
		IdempotencyKey: "request-1",
	}
	first, err := store.Append(ctx, input)
	if err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	second, err := store.Append(ctx, input)
	if err != nil {
		t.Fatalf("second Append returned error: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("idempotent append created different events: %q != %q", first.ID, second.ID)
	}
	if err := store.Checkpoint(ctx); err != nil {
		t.Fatalf("Checkpoint returned error: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	databaseBytes, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if bytes.Contains(databaseBytes, marker) {
		t.Fatal("SQLite database contains plaintext payload marker")
	}

	reopened, err := Open(databasePath, sealer)
	if err != nil {
		t.Fatalf("reopen returned error: %v", err)
	}
	defer reopened.Close()
	restored, err := reopened.Get(ctx, first.ID)
	if err != nil {
		t.Fatalf("Get after restart returned error: %v", err)
	}
	if !bytes.Equal(restored.Payload, marker) {
		t.Fatalf("restored payload mismatch: %q", restored.Payload)
	}
}

func TestSecretPayloadNeverReachesStorage(t *testing.T) {
	t.Parallel()

	sealer, err := envelope.NewSealer("test-key", bytes.Repeat([]byte{0x17}, 32))
	if err != nil {
		t.Fatalf("NewSealer returned error: %v", err)
	}
	store, err := Open(filepath.Join(t.TempDir(), "vault.db"), sealer)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	_, err = store.Append(context.Background(), eventstore.AppendInput{
		VaultID:        "vault-test",
		Type:           "secret.detected",
		SchemaVersion:  1,
		Sensitivity:    event.SensitivitySecret,
		Payload:        []byte("forbidden"),
		IdempotencyKey: "secret-request",
	})
	if !errors.Is(err, event.ErrSecretPayload) {
		t.Fatalf("expected ErrSecretPayload, got %v", err)
	}
}

func TestIdempotencyKeyRejectsDifferentRequest(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	defer store.Close()
	ctx := context.Background()
	first, err := store.Append(ctx, eventstore.AppendInput{
		VaultID:        "vault-test",
		Type:           "task.created",
		SchemaVersion:  1,
		Sensitivity:    event.SensitivityPrivate,
		Payload:        []byte("first payload"),
		IdempotencyKey: "request-conflict",
	})
	if err != nil {
		t.Fatalf("first Append returned error: %v", err)
	}

	_, err = store.Append(ctx, eventstore.AppendInput{
		VaultID:        "vault-test",
		Type:           "task.created",
		SchemaVersion:  1,
		Sensitivity:    event.SensitivityPrivate,
		Payload:        []byte("different payload"),
		IdempotencyKey: "request-conflict",
	})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected ErrIdempotencyConflict, got %v", err)
	}

	restored, err := store.Get(ctx, first.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if !bytes.Equal(restored.Payload, []byte("first payload")) {
		t.Fatalf("idempotency conflict mutated the original event: %q", restored.Payload)
	}
}

func TestInvalidRetryCannotReuseExistingIdempotencyResult(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	defer store.Close()
	ctx := context.Background()
	_, err := store.Append(ctx, eventstore.AppendInput{
		VaultID:        "vault-test",
		Type:           "task.created",
		SchemaVersion:  1,
		Sensitivity:    event.SensitivityPrivate,
		Payload:        []byte("allowed"),
		IdempotencyKey: "request-shared",
	})
	if err != nil {
		t.Fatalf("first Append returned error: %v", err)
	}

	_, err = store.Append(ctx, eventstore.AppendInput{
		VaultID:        "vault-test",
		Type:           "secret.detected",
		SchemaVersion:  1,
		Sensitivity:    event.SensitivitySecret,
		Payload:        []byte("forbidden"),
		IdempotencyKey: "request-shared",
	})
	if !errors.Is(err, event.ErrSecretPayload) {
		t.Fatalf("expected ErrSecretPayload before idempotency lookup, got %v", err)
	}
}

func TestLegacyIdempotencyResultFailsClosed(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	defer store.Close()
	ctx := context.Background()
	input := eventstore.AppendInput{
		VaultID:        "vault-test",
		Type:           "task.created",
		SchemaVersion:  1,
		Sensitivity:    event.SensitivityPrivate,
		Payload:        []byte("legacy request"),
		IdempotencyKey: "legacy-request",
	}
	if _, err := store.Append(ctx, input); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	if _, err := store.db.Exec("UPDATE idempotency_keys SET request_hash = NULL WHERE idempotency_key = ?", input.IdempotencyKey); err != nil {
		t.Fatalf("clear request hash: %v", err)
	}

	_, err := store.Append(ctx, input)
	if !errors.Is(err, ErrIdempotencyUnverifiable) {
		t.Fatalf("expected ErrIdempotencyUnverifiable, got %v", err)
	}
}

func TestOpenMigratesLegacyUnversionedDatabase(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	legacyStatements := []string{
		`CREATE TABLE events (
			event_id TEXT PRIMARY KEY,
			vault_id TEXT NOT NULL,
			event_type TEXT NOT NULL,
			schema_version INTEGER NOT NULL CHECK (schema_version > 0),
			sensitivity TEXT NOT NULL CHECK (sensitivity IN ('public','private','sensitive','secret')),
			payload_envelope BLOB NOT NULL,
			occurred_at TEXT NOT NULL
		) STRICT`,
		`CREATE TABLE idempotency_keys (
			idempotency_key TEXT PRIMARY KEY,
			event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT
		) STRICT`,
	}
	for _, statement := range legacyStatements {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			t.Fatalf("create legacy database: %v", err)
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
	if version != currentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, currentSchemaVersion)
	}
	rows, err := store.db.Query("PRAGMA table_info(idempotency_keys)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	foundRequestHash := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if name == "request_hash" && dataType == "BLOB" {
			foundRequestHash = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !foundRequestHash {
		t.Fatal("legacy database was not migrated with request_hash")
	}
}

func TestSchema24IndexesEventsAndMarksLegacyRecoveryComplete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "schema-24.db")
	store := openTestStore(t, databasePath)
	indexNames := make(map[string]bool)
	rows, err := store.db.QueryContext(ctx, "PRAGMA index_list(events)")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			t.Fatal(err)
		}
		indexNames[name] = true
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"events_vault_order_idx", "events_schema_type_order_idx"} {
		if !indexNames[name] {
			t.Fatalf("missing event index %q: %+v", name, indexNames)
		}
	}
	assertQueryPlanUsesIndex(t, store.db, "events_vault_order_idx", `SELECT event_id FROM events WHERE vault_id = ? ORDER BY occurred_at, event_id`, "vault-test")
	assertQueryPlanUsesIndex(t, store.db, "events_schema_type_order_idx", `SELECT event_id FROM events WHERE schema_version = ? AND event_type = ? ORDER BY occurred_at, event_id`, 1, "task.created")
	var firstCompletedAt string
	if err := store.db.QueryRowContext(ctx, `SELECT completed_at FROM recovery_markers WHERE recovery_key = ? AND completed_schema_version = ?`, legacyPortableRecoveryKey, currentSchemaVersion).Scan(&firstCompletedAt); err != nil {
		t.Fatalf("read recovery marker: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTestStore(t, databasePath)
	defer reopened.Close()
	var reopenedCompletedAt string
	if err := reopened.db.QueryRowContext(ctx, `SELECT completed_at FROM recovery_markers WHERE recovery_key = ?`, legacyPortableRecoveryKey).Scan(&reopenedCompletedAt); err != nil {
		t.Fatal(err)
	}
	if reopenedCompletedAt != firstCompletedAt {
		t.Fatalf("one-time recovery marker changed on reopen: %q != %q", reopenedCompletedAt, firstCompletedAt)
	}
}

func TestOpenRejectsLegacyRecoveryMarkerSchemaMismatch(t *testing.T) {
	t.Parallel()
	databasePath := filepath.Join(t.TempDir(), "recovery-marker-mismatch.db")
	store := openTestStore(t, databasePath)
	if _, err := store.db.Exec(`UPDATE recovery_markers SET completed_schema_version = ? WHERE recovery_key = ?`, currentSchemaVersion-1, legacyPortableRecoveryKey); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	sealer, err := envelope.NewSealer("test-key", bytes.Repeat([]byte{0x23}, 32))
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(databasePath, sealer)
	if reopened != nil {
		_ = reopened.Close()
		t.Fatal("Open accepted a mismatched recovery marker")
	}
	if err == nil || !strings.Contains(err.Error(), "recovery marker schema mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertQueryPlanUsesIndex(t *testing.T, db *sql.DB, indexName, query string, arguments ...any) {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+query, arguments...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(details, "\n"), indexName) {
		t.Fatalf("query plan did not use %s: %v", indexName, details)
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "future.db")
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 999"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	sealer, err := envelope.NewSealer("test-key", bytes.Repeat([]byte{0x29}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(databasePath, sealer)
	if store != nil {
		_ = store.Close()
		t.Fatal("Open returned a store for a newer schema")
	}
	if !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("expected ErrUnsupportedSchema, got %v", err)
	}
}

func TestOpenRejectsSchemaVersionWithMissingColumns(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "malformed.db")
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		"CREATE TABLE events(event_id TEXT PRIMARY KEY) STRICT",
		"CREATE TABLE idempotency_keys(idempotency_key TEXT PRIMARY KEY, event_id TEXT, request_hash BLOB) STRICT",
		"PRAGMA user_version = 2",
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	sealer, err := envelope.NewSealer("test-key", bytes.Repeat([]byte{0x31}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(databasePath, sealer)
	if store != nil {
		_ = store.Close()
		t.Fatal("Open returned a store for a malformed schema")
	}
	if err == nil || !strings.Contains(err.Error(), "sqlite event store") {
		t.Fatalf("expected schema validation error, got %v", err)
	}
}

func openTestStore(t *testing.T, databasePath string) *Store {
	t.Helper()
	sealer, err := envelope.NewSealer("test-key", bytes.Repeat([]byte{0x23}, 32))
	if err != nil {
		t.Fatalf("NewSealer returned error: %v", err)
	}
	store, err := Open(databasePath, sealer)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	return store
}
