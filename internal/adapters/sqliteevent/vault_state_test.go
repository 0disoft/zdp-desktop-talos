package sqliteevent

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
)

func TestVaultStateAndEventsCommitAtomicallyAndSurviveRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "vault.db")
	store := openTestStore(t, databasePath)
	createdAt := time.Unix(1_720_000_000, 0).UTC()

	created, err := store.CreateVault(ctx, vaultstore.CreateInput{
		VaultID:        "vault-alpha",
		RetentionDays:  30,
		OccurredAt:     createdAt,
		IdempotencyKey: "create-vault-alpha",
	})
	if err != nil {
		t.Fatalf("CreateVault returned error: %v", err)
	}
	if created.Revision != 1 || created.RetentionDays != 30 {
		t.Fatalf("unexpected created state: %+v", created)
	}
	replayed, err := store.CreateVault(ctx, vaultstore.CreateInput{
		VaultID:        "vault-alpha",
		RetentionDays:  30,
		OccurredAt:     createdAt,
		IdempotencyKey: "create-vault-alpha",
	})
	if err != nil {
		t.Fatalf("idempotent CreateVault returned error: %v", err)
	}
	if replayed != created {
		t.Fatalf("idempotent create returned a different result: %+v != %+v", replayed, created)
	}

	updatedAt := createdAt.Add(time.Minute)
	updated, err := store.UpdateVaultRetention(ctx, vaultstore.UpdateRetentionInput{
		VaultID:          "vault-alpha",
		ExpectedRevision: 1,
		RetentionDays:    60,
		OccurredAt:       updatedAt,
		IdempotencyKey:   "retention-vault-alpha-2",
	})
	if err != nil {
		t.Fatalf("UpdateVaultRetention returned error: %v", err)
	}
	if updated.Revision != 2 || updated.RetentionDays != 60 || updated.LastEventID == created.LastEventID {
		t.Fatalf("unexpected updated state: %+v", updated)
	}
	if err := store.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openTestStore(t, databasePath)
	defer reopened.Close()
	restored, err := reopened.GetVault(ctx, "vault-alpha")
	if err != nil {
		t.Fatalf("GetVault after restart returned error: %v", err)
	}
	if restored != updated {
		t.Fatalf("restored state mismatch: %+v != %+v", restored, updated)
	}

	rows, err := reopened.db.Query("SELECT payload_envelope FROM events WHERE vault_id = ?", "vault-alpha")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var encrypted []byte
		if err := rows.Scan(&encrypted); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(encrypted, []byte(`"retention_days"`)) || bytes.Contains(encrypted, []byte(updatedAt.Format(time.RFC3339Nano))) {
			t.Fatal("event payload contains plaintext Vault state")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestVaultCommandIdempotencyRejectsChangedIntent(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	defer store.Close()
	ctx := context.Background()
	input := vaultstore.CreateInput{
		VaultID:        "vault-alpha",
		RetentionDays:  30,
		IdempotencyKey: "create-vault-alpha",
	}
	if _, err := store.CreateVault(ctx, input); err != nil {
		t.Fatal(err)
	}
	input.RetentionDays = 60
	if _, err := store.CreateVault(ctx, input); !errors.Is(err, vaultstore.ErrIdempotencyConflict) {
		t.Fatalf("expected ErrIdempotencyConflict, got %v", err)
	}
}

func TestStaleVaultRevisionDoesNotAppendEvent(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	defer store.Close()
	ctx := context.Background()
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{
		VaultID:        "vault-alpha",
		RetentionDays:  30,
		IdempotencyKey: "create-vault-alpha",
	}); err != nil {
		t.Fatal(err)
	}
	before := tableCount(t, store, "events")
	_, err := store.UpdateVaultRetention(ctx, vaultstore.UpdateRetentionInput{
		VaultID:          "vault-alpha",
		ExpectedRevision: 99,
		RetentionDays:    60,
		IdempotencyKey:   "stale-retention",
	})
	if !errors.Is(err, vaultstore.ErrRevisionConflict) {
		t.Fatalf("expected ErrRevisionConflict, got %v", err)
	}
	if after := tableCount(t, store, "events"); after != before {
		t.Fatalf("stale update appended an event: before=%d after=%d", before, after)
	}
}

func TestVaultStateFailureRollsBackEventAndIdempotency(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	defer store.Close()
	if _, err := store.db.Exec(`CREATE TRIGGER reject_vault_state BEFORE INSERT ON vault_states
		BEGIN SELECT RAISE(ABORT, 'forced vault state failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := store.CreateVault(context.Background(), vaultstore.CreateInput{
		VaultID:        "vault-alpha",
		RetentionDays:  30,
		IdempotencyKey: "create-vault-alpha",
	})
	if err == nil {
		t.Fatal("CreateVault succeeded despite forced state failure")
	}
	for _, table := range []string{"events", "idempotency_keys", "vault_states"} {
		if count := tableCount(t, store, table); count != 0 {
			t.Fatalf("%s contains %d rows after rollback", table, count)
		}
	}
}

func TestConcurrentVaultRetentionUpdatesAllowOneRevisionWinner(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	defer store.Close()
	ctx := context.Background()
	created, err := store.CreateVault(ctx, vaultstore.CreateInput{
		VaultID:        "vault-alpha",
		RetentionDays:  30,
		IdempotencyKey: "create-vault-alpha",
	})
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for index, days := range []int{60, 90} {
		index, days := index, days
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := store.UpdateVaultRetention(ctx, vaultstore.UpdateRetentionInput{
				VaultID:          "vault-alpha",
				ExpectedRevision: created.Revision,
				RetentionDays:    days,
				IdempotencyKey:   "concurrent-retention-" + string(rune('a'+index)),
			})
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, vaultstore.ErrRevisionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent update result: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results: successes=%d conflicts=%d", successes, conflicts)
	}
	state, err := store.GetVault(ctx, "vault-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if state.Revision != 2 || state.RetentionDays != 60 && state.RetentionDays != 90 {
		t.Fatalf("unexpected winning state: %+v", state)
	}
}

func tableCount(t *testing.T, store *Store, table string) int {
	t.Helper()
	query := "SELECT COUNT(*) FROM " + table
	var count int
	if err := store.db.QueryRow(query).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
