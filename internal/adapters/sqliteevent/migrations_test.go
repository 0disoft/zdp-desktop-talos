package sqliteevent

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestApplyMigrationRollsBackDDLAndVersionWhenCommitFails(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "migration-rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}

	err = applyMigration(context.Background(), db, migration{version: 1, statements: []string{
		`CREATE TABLE migration_parent(id INTEGER PRIMARY KEY) STRICT`,
		`CREATE TABLE migration_child(id INTEGER PRIMARY KEY, parent_id INTEGER NOT NULL REFERENCES migration_parent(id) DEFERRABLE INITIALLY DEFERRED) STRICT`,
		`INSERT INTO migration_child(id, parent_id) VALUES(1, 99)`,
	}})
	if err == nil {
		t.Fatal("migration with a deferred foreign-key violation committed")
	}
	version, versionErr := schemaVersion(context.Background(), db)
	if versionErr != nil {
		t.Fatal(versionErr)
	}
	if version != 0 {
		t.Fatalf("schema version=%d, want 0", version)
	}
	for _, table := range []string{"migration_parent", "migration_child"} {
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_schema WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("table %q survived rollback: name=%q error=%v", table, name, err)
		}
	}
}
