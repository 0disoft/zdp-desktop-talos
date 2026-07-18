package dpapicatalog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultcatalog"
)

func TestCatalogRoundTripOrderingAndExactRemoval(t *testing.T) {
	t.Parallel()
	keys := &memoryKeyStore{values: map[keyvault.Reference][]byte{}}
	catalog, err := New(keys)
	if err != nil {
		t.Fatal(err)
	}
	newer := vaultcatalog.Entry{VaultID: "00000000-0000-7000-8000-000000000002", CreatedAt: time.Unix(200, 0).UTC()}
	older := vaultcatalog.Entry{VaultID: "00000000-0000-7000-8000-000000000001", CreatedAt: time.Unix(100, 0).UTC()}
	newerActive := newer
	newerActive.State = vaultcatalog.StateActive
	olderActive := older
	olderActive.State = vaultcatalog.StateActive
	if err := catalog.Add(context.Background(), newer); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Add(context.Background(), older); err != nil {
		t.Fatal(err)
	}
	entries, err := catalog.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0] != olderActive || entries[1] != newerActive {
		t.Fatalf("entries = %+v", entries)
	}
	wrongRevision := older
	wrongRevision.CreatedAt = wrongRevision.CreatedAt.Add(time.Second)
	if err := catalog.Remove(context.Background(), wrongRevision); !errors.Is(err, vaultcatalog.ErrNotFound) {
		t.Fatalf("inexact removal error = %v", err)
	}
	if err := catalog.Remove(context.Background(), older); err != nil {
		t.Fatal(err)
	}
	entries, err = catalog.List(context.Background())
	if err != nil || len(entries) != 1 || entries[0] != newerActive {
		t.Fatalf("entries after removal = %+v, err=%v", entries, err)
	}
}

func TestCatalogMigratesV1AndSeparatesPendingPurges(t *testing.T) {
	t.Parallel()
	keys := &memoryKeyStore{values: map[keyvault.Reference][]byte{
		catalogReference: []byte(`{"version":1,"entries":[{"vault_id":"00000000-0000-7000-8000-000000000001","created_at":"1970-01-01T00:01:40Z"}]}`),
	}}
	catalog, _ := New(keys)
	entries, err := catalog.List(context.Background())
	if err != nil || len(entries) != 1 || entries[0].State != vaultcatalog.StateActive {
		t.Fatalf("v1 entries=%+v err=%v", entries, err)
	}
	if err := catalog.MarkPurgePending(context.Background(), entries[0]); err != nil {
		t.Fatal(err)
	}
	if active, err := catalog.List(context.Background()); err != nil || len(active) != 0 {
		t.Fatalf("active=%+v err=%v", active, err)
	}
	pending, err := catalog.PendingPurges(context.Background())
	if err != nil || len(pending) != 1 || pending[0].State != vaultcatalog.StatePurgePending {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	if err := catalog.MarkPurgePending(context.Background(), pending[0]); err != nil {
		t.Fatalf("idempotent pending transition: %v", err)
	}
}

func TestCatalogJournalsRestoreAndOnlyReturnsToActive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	keys := &memoryKeyStore{values: map[keyvault.Reference][]byte{}}
	catalog, _ := New(keys)
	entry := vaultcatalog.Entry{VaultID: "00000000-0000-7000-8000-000000000001", CreatedAt: time.Unix(100, 0).UTC()}
	if err := catalog.Add(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if err := catalog.MarkRestorePending(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if active, err := catalog.List(ctx); err != nil || len(active) != 0 {
		t.Fatalf("active=%+v err=%v", active, err)
	}
	pending, err := catalog.PendingRestores(ctx)
	if err != nil || len(pending) != 1 || pending[0].State != vaultcatalog.StateRestorePending {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	if err := catalog.MarkPurgePending(ctx, pending[0]); !errors.Is(err, vaultcatalog.ErrNotFound) {
		t.Fatalf("restore-to-purge transition error=%v", err)
	}
	if err := catalog.MarkActive(ctx, pending[0]); err != nil {
		t.Fatal(err)
	}
	active, err := catalog.List(ctx)
	if err != nil || len(active) != 1 || active[0].State != vaultcatalog.StateActive {
		t.Fatalf("active after restore=%+v err=%v", active, err)
	}
	if err := catalog.MarkActive(ctx, active[0]); err != nil {
		t.Fatalf("idempotent active transition: %v", err)
	}
}

func TestCatalogRejectsDuplicateAndCorruptDocument(t *testing.T) {
	t.Parallel()
	keys := &memoryKeyStore{values: map[keyvault.Reference][]byte{}}
	catalog, _ := New(keys)
	entry := vaultcatalog.Entry{VaultID: "00000000-0000-7000-8000-000000000001", CreatedAt: time.Unix(100, 0).UTC()}
	if err := catalog.Add(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Add(context.Background(), entry); !errors.Is(err, vaultcatalog.ErrConflict) {
		t.Fatalf("duplicate error = %v", err)
	}
	keys.values[catalogReference] = []byte(`{"version":1,"entries":[{"vault_id":"00000000-0000-7000-8000-000000000001","created_at":"bad"}]}`)
	if _, err := catalog.List(context.Background()); !errors.Is(err, vaultcatalog.ErrCorrupt) {
		t.Fatalf("corrupt document error = %v", err)
	}
}

type memoryKeyStore struct{ values map[keyvault.Reference][]byte }

func (s *memoryKeyStore) Put(_ context.Context, ref keyvault.Reference, value []byte) error {
	if _, exists := s.values[ref]; exists {
		return keyvault.ErrAlreadyExists
	}
	s.values[ref] = append([]byte(nil), value...)
	return nil
}
func (s *memoryKeyStore) Get(_ context.Context, ref keyvault.Reference) ([]byte, error) {
	value, exists := s.values[ref]
	if !exists {
		return nil, keyvault.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}
func (s *memoryKeyStore) Rotate(_ context.Context, ref keyvault.Reference, value []byte) error {
	if _, exists := s.values[ref]; !exists {
		return keyvault.ErrNotFound
	}
	s.values[ref] = append([]byte(nil), value...)
	return nil
}
func (s *memoryKeyStore) Delete(_ context.Context, ref keyvault.Reference) error {
	if _, exists := s.values[ref]; !exists {
		return keyvault.ErrNotFound
	}
	delete(s.values, ref)
	return nil
}
