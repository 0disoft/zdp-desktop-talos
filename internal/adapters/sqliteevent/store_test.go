package sqliteevent

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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
