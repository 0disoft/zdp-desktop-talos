package syncexport_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/sqliteevent"
	"github.com/0disoft/zdp-desktop-talos/internal/application/syncexport"
	"github.com/0disoft/zdp-desktop-talos/internal/application/syncidentity"
	"github.com/0disoft/zdp-desktop-talos/internal/application/syncpack"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/eventstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/syncstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
	"github.com/0disoft/zdp-desktop-talos/internal/security/envelope"
	"github.com/0disoft/zdp-desktop-talos/internal/security/redaction"
)

func TestExporterPersistsOneDeviceIdentityAndContiguousReadyPacks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "vault.db")
	vaultKey := bytes.Repeat([]byte{0x52}, 32)
	store := openStore(t, databasePath, vaultKey)
	now := time.Date(2026, 7, 18, 13, 0, 0, 0, time.UTC)
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: "vault-e2e", RetentionDays: 30, OccurredAt: now, IdempotencyKey: "vault-e2e"}); err != nil {
		t.Fatal(err)
	}
	keys := &memoryKeys{values: map[keyvault.Reference][]byte{{VaultID: "vault-e2e", KeyID: "vault-kek-v1"}: append([]byte(nil), vaultKey...)}}
	appendEvent := func(key, value string, at time.Time) {
		t.Helper()
		if _, err := store.Append(ctx, eventstore.AppendInput{VaultID: "vault-e2e", Type: "task.contract.created", SchemaVersion: 2, Sensitivity: event.SensitivityPrivate, Payload: []byte(`{"value":"` + value + `"}`), OccurredAt: at, IdempotencyKey: key}); err != nil {
			t.Fatal(err)
		}
	}
	appendEvent("event-1", "first", now.Add(time.Second))
	exporter, err := syncexport.New(syncpack.New(), store, keys, redaction.NewScanner(), "vault-kek-v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exporter.ExportNext(ctx, "vault-e2e", 32); !errors.Is(err, syncidentity.ErrNotInitialized) {
		t.Fatalf("export before explicit initialization error=%v", err)
	}
	identity, err := syncidentity.New(keys, store)
	if err != nil {
		t.Fatal(err)
	}
	local, err := identity.Ensure(ctx, "vault-e2e")
	if err != nil {
		t.Fatal(err)
	}
	clear(local.PrivateKey)
	first, err := exporter.ExportNext(ctx, "vault-e2e", 32)
	if err != nil || first.Batch.SequenceStart != 1 || first.Batch.SequenceEnd != 1 || first.Manifest.DeviceID == "" || len(first.Encoded) == 0 {
		t.Fatalf("first=%+v error=%v", first, err)
	}
	device, err := store.GetSyncDevice(ctx, "vault-e2e", first.Manifest.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	manifest, imported, err := syncpack.New().Import(ctx, syncpack.ImportInput{Encoded: first.Encoded, VaultID: "vault-e2e", DeviceID: first.Manifest.DeviceID, EncryptionKey: vaultKey, VerifyKey: device.PublicKey})
	if err != nil || manifest.PackID != first.Manifest.PackID || len(imported) != 1 || imported[0].Type != "task.contract.created" || imported[0].DeviceSeq != 1 {
		t.Fatalf("manifest=%+v imported=%+v error=%v", manifest, imported, err)
	}
	if _, err := exporter.ExportNext(ctx, "vault-e2e", 32); !errors.Is(err, syncstore.ErrNoExportableEvents) {
		t.Fatalf("empty export error=%v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openStore(t, databasePath, vaultKey)
	defer reopened.Close()
	appendEvent = func(key, value string, at time.Time) {
		t.Helper()
		if _, err := reopened.Append(ctx, eventstore.AppendInput{VaultID: "vault-e2e", Type: "memory.state.changed", SchemaVersion: 2, Sensitivity: event.SensitivityPrivate, Payload: []byte(`{"value":"` + value + `"}`), OccurredAt: at, IdempotencyKey: key}); err != nil {
			t.Fatal(err)
		}
	}
	appendEvent("event-2", "second", now.Add(2*time.Second))
	restartedExporter, err := syncexport.New(syncpack.New(), reopened, keys, redaction.NewScanner(), "vault-kek-v1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := restartedExporter.ExportNext(ctx, "vault-e2e", 32)
	if err != nil || second.Manifest.DeviceID != first.Manifest.DeviceID || second.Batch.SequenceStart != 2 || second.Batch.SequenceEnd != 2 {
		t.Fatalf("second=%+v error=%v", second, err)
	}
	if keys.putCount(syncidentity.SigningKeyID) != 1 {
		t.Fatalf("sync signing key put count=%d", keys.putCount(syncidentity.SigningKeyID))
	}
	if _, err := reopened.Append(ctx, eventstore.AppendInput{VaultID: "vault-e2e", Type: "memory.state.changed", SchemaVersion: 2, Sensitivity: event.SensitivityPrivate, Payload: []byte(`{"password":"test-only-marker"}`), OccurredAt: now.Add(3 * time.Second), IdempotencyKey: "event-secret-finding"}); err != nil {
		t.Fatal(err)
	}
	if _, err := restartedExporter.ExportNext(ctx, "vault-e2e", 32); !errors.Is(err, syncexport.ErrSecretFindings) {
		t.Fatalf("secret finding export error=%v", err)
	}
}

func openStore(t *testing.T, path string, key []byte) *sqliteevent.Store {
	t.Helper()
	sealer, err := envelope.NewSealer("test-vault-key", key)
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqliteevent.Open(path, sealer)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

type memoryKeys struct {
	mu     sync.Mutex
	values map[keyvault.Reference][]byte
	puts   map[string]int
}

func (s *memoryKeys) Put(_ context.Context, ref keyvault.Reference, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.values[ref]; exists {
		return keyvault.ErrAlreadyExists
	}
	if s.puts == nil {
		s.puts = map[string]int{}
	}
	s.puts[ref.KeyID]++
	s.values[ref] = append([]byte(nil), value...)
	return nil
}

func (s *memoryKeys) Get(_ context.Context, ref keyvault.Reference) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, exists := s.values[ref]
	if !exists {
		return nil, keyvault.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s *memoryKeys) Rotate(_ context.Context, ref keyvault.Reference, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.values[ref]; !exists {
		return keyvault.ErrNotFound
	}
	s.values[ref] = append([]byte(nil), value...)
	return nil
}

func (s *memoryKeys) Delete(_ context.Context, ref keyvault.Reference) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.values[ref]; !exists {
		return keyvault.ErrNotFound
	}
	delete(s.values, ref)
	return nil
}

func (s *memoryKeys) putCount(keyID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.puts[keyID]
}
