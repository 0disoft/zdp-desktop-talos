package vaultbootstrap

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/vault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultcatalog"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultdb"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
)

func TestCreatePersistsKeyBeforeInitializingVault(t *testing.T) {
	t.Parallel()
	keys := &fakeKeyStore{}
	database := &fakeDatabase{}
	databases := &fakeDatabaseFactory{database: database}
	catalog := &fakeCatalog{}
	creator, err := NewCreator(keys, databases, catalog)
	if err != nil {
		t.Fatal(err)
	}
	creator.now = func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }
	creator.random = bytes.NewReader(testRandomBytes())

	session, err := creator.Create(context.Background(), CreateInput{RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if !keys.present || !databases.created || database.input.VaultID != session.Record.ID {
		t.Fatalf("incomplete creation: keys=%+v databases=%+v input=%+v", keys, databases, database.input)
	}
	if len(catalog.entries) != 1 || catalog.entries[0].VaultID != session.Record.ID {
		t.Fatalf("Vault was not cataloged: %+v", catalog.entries)
	}
	if database.input.IdempotencyKey != "vault-bootstrap:"+session.Record.ID || session.Record.Revision != 1 {
		t.Fatalf("unexpected session: %+v, input=%+v", session.Record, database.input)
	}
	if len(databases.keySnapshot) != vaultKeyBytes || bytes.Equal(databases.keySnapshot, make([]byte, vaultKeyBytes)) {
		t.Fatal("database factory did not receive generated key bytes")
	}
}

func TestCreateRemovesKeyWhenDatabaseCreationFails(t *testing.T) {
	t.Parallel()
	keys := &fakeKeyStore{}
	databases := &fakeDatabaseFactory{createErr: errors.New("disk full")}
	creator, _ := NewCreator(keys, databases, &fakeCatalog{})
	creator.now = func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }
	creator.random = bytes.NewReader(testRandomBytes())

	if _, err := creator.Create(context.Background(), CreateInput{RetentionDays: 30}); err == nil || errors.Is(err, ErrCompensationFailed) {
		t.Fatalf("unexpected error: %v", err)
	}
	if keys.present || !keys.deleted || !databases.removed {
		t.Fatalf("partial creation was not compensated: keys=%+v databases=%+v", keys, databases)
	}
}

func TestCreateRemovesDatabaseAndKeyWhenStateInitializationFails(t *testing.T) {
	t.Parallel()
	keys := &fakeKeyStore{}
	database := &fakeDatabase{createErr: errors.New("transaction failed")}
	databases := &fakeDatabaseFactory{database: database}
	creator, _ := NewCreator(keys, databases, &fakeCatalog{})
	creator.now = func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }
	creator.random = bytes.NewReader(testRandomBytes())

	if _, err := creator.Create(context.Background(), CreateInput{RetentionDays: 30}); err == nil {
		t.Fatal("state initialization failure was hidden")
	}
	if keys.present || !keys.deleted || !databases.removed || !database.closed {
		t.Fatalf("partial creation was not compensated: keys=%+v databases=%+v database=%+v", keys, databases, database)
	}
}

func TestCreateReportsIncompleteCompensation(t *testing.T) {
	t.Parallel()
	keys := &fakeKeyStore{deleteErr: errors.New("access denied")}
	databases := &fakeDatabaseFactory{createErr: errors.New("disk full"), removeErr: errors.New("file busy")}
	creator, _ := NewCreator(keys, databases, &fakeCatalog{})
	creator.now = func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }
	creator.random = bytes.NewReader(testRandomBytes())

	_, err := creator.Create(context.Background(), CreateInput{RetentionDays: 30})
	if !errors.Is(err, ErrCompensationFailed) || !errors.Is(err, keys.deleteErr) || !errors.Is(err, databases.removeErr) {
		t.Fatalf("compensation error lost evidence: %v", err)
	}
}

func TestCreateCompensatesCatalogDatabaseAndKeyWhenCatalogWriteFails(t *testing.T) {
	t.Parallel()
	keys := &fakeKeyStore{}
	database := &fakeDatabase{}
	databases := &fakeDatabaseFactory{database: database}
	catalog := &fakeCatalog{addErr: errors.New("catalog write failed")}
	creator, _ := NewCreator(keys, databases, catalog)
	creator.now = func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }
	creator.random = bytes.NewReader(testRandomBytes())

	if _, err := creator.Create(context.Background(), CreateInput{RetentionDays: 30}); err == nil {
		t.Fatal("catalog failure was hidden")
	}
	if keys.present || !database.closed || !databases.removed || catalog.removed.VaultID == "" {
		t.Fatalf("catalog failure was not compensated: keys=%+v db=%+v factory=%+v catalog=%+v", keys, database, databases, catalog)
	}
}

func TestOpenRequiresCatalogAndLoadsMatchingVault(t *testing.T) {
	t.Parallel()
	createdAt := time.Unix(1_800_000_000, 0).UTC()
	vaultID := "00000000-0000-7000-8000-000000000001"
	keys := &fakeKeyStore{value: bytes.Repeat([]byte{3}, vaultKeyBytes), present: true}
	database := &fakeDatabase{stored: vault.Record{
		ID: vaultID, Revision: 2, Status: vault.StatusActive, RetentionDays: 90,
		CreatedAt: createdAt, UpdatedAt: createdAt.Add(time.Hour), LastEventID: "event-2",
	}}
	databases := &fakeDatabaseFactory{database: database}
	creator, _ := NewCreator(keys, databases, &fakeCatalog{entries: []vaultcatalog.Entry{{VaultID: vaultID, CreatedAt: createdAt}}})

	if _, err := creator.Open(context.Background(), "unknown"); !errors.Is(err, ErrNotCataloged) {
		t.Fatalf("unknown Vault error = %v", err)
	}
	session, err := creator.Open(context.Background(), vaultID)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if !databases.opened || session.Record != database.stored {
		t.Fatalf("session=%+v database=%+v factory=%+v", session.Record, database.stored, databases)
	}
}

func TestOpenRejectsUnknownAndMismatchedVault(t *testing.T) {
	t.Parallel()
	createdAt := time.Unix(1_800_000_000, 0).UTC()
	keys := &fakeKeyStore{value: bytes.Repeat([]byte{9}, vaultKeyBytes), present: true}
	database := &fakeDatabase{stored: vault.Record{
		ID: "00000000-0000-7000-8000-000000000002", Revision: 1, Status: vault.StatusActive, RetentionDays: 30,
		CreatedAt: createdAt, UpdatedAt: createdAt, LastEventID: "event-1",
	}}
	databases := &fakeDatabaseFactory{database: database}
	creator, _ := NewCreator(keys, databases, &fakeCatalog{entries: []vaultcatalog.Entry{{VaultID: "00000000-0000-7000-8000-000000000001", CreatedAt: createdAt}}})

	if _, err := creator.Open(context.Background(), "00000000-0000-7000-8000-000000000003"); !errors.Is(err, ErrNotCataloged) || databases.opened {
		t.Fatalf("unknown open error=%v factory=%+v", err, databases)
	}
	if _, err := creator.Open(context.Background(), "00000000-0000-7000-8000-000000000001"); !errors.Is(err, ErrNotCataloged) || !database.closed {
		t.Fatalf("mismatched open error=%v database=%+v", err, database)
	}
}

type fakeKeyStore struct {
	present   bool
	deleted   bool
	deleteErr error
	value     []byte
}

func testRandomBytes() []byte {
	data := make([]byte, 48)
	data[16] = 1
	return data
}

func (s *fakeKeyStore) Put(_ context.Context, _ keyvault.Reference, value []byte) error {
	s.present = true
	s.value = append([]byte(nil), value...)
	return nil
}
func (s *fakeKeyStore) Get(context.Context, keyvault.Reference) ([]byte, error) {
	if !s.present {
		return nil, keyvault.ErrNotFound
	}
	return append([]byte(nil), s.value...), nil
}
func (s *fakeKeyStore) Rotate(context.Context, keyvault.Reference, []byte) error { return nil }
func (s *fakeKeyStore) Delete(context.Context, keyvault.Reference) error {
	s.deleted = true
	if s.deleteErr == nil {
		s.present = false
	}
	return s.deleteErr
}

type fakeDatabaseFactory struct {
	database    vaultdb.Database
	createErr   error
	removeErr   error
	created     bool
	opened      bool
	removed     bool
	keySnapshot []byte
}

func (f *fakeDatabaseFactory) Create(_ context.Context, _ string, _ string, key []byte) (vaultdb.Database, error) {
	f.created = true
	f.keySnapshot = append([]byte(nil), key...)
	return f.database, f.createErr
}
func (f *fakeDatabaseFactory) Open(context.Context, string, string, []byte) (vaultdb.Database, error) {
	f.opened = true
	return f.database, nil
}
func (f *fakeDatabaseFactory) Remove(context.Context, string) error {
	f.removed = true
	return f.removeErr
}

type fakeDatabase struct {
	input     vaultstore.CreateInput
	createErr error
	closed    bool
	stored    vault.Record
}

func (d *fakeDatabase) CreateVault(_ context.Context, input vaultstore.CreateInput) (vault.Record, error) {
	d.input = input
	if d.createErr != nil {
		return vault.Record{}, d.createErr
	}
	return vault.Record{ID: input.VaultID, Revision: 1, Status: vault.StatusActive, RetentionDays: input.RetentionDays, CreatedAt: input.OccurredAt, UpdatedAt: input.OccurredAt, LastEventID: "event-1"}, nil
}
func (d *fakeDatabase) GetVault(context.Context, string) (vault.Record, error) {
	return d.stored, nil
}
func (d *fakeDatabase) UpdateVaultRetention(context.Context, vaultstore.UpdateRetentionInput) (vault.Record, error) {
	return vault.Record{}, nil
}
func (d *fakeDatabase) Close() error { d.closed = true; return nil }

type fakeCatalog struct {
	entries []vaultcatalog.Entry
	addErr  error
	removed vaultcatalog.Entry
}

func (c *fakeCatalog) List(context.Context) ([]vaultcatalog.Entry, error) {
	return append([]vaultcatalog.Entry(nil), c.entries...), nil
}
func (c *fakeCatalog) Add(_ context.Context, entry vaultcatalog.Entry) error {
	if c.addErr != nil {
		return c.addErr
	}
	c.entries = append(c.entries, entry)
	return nil
}
func (c *fakeCatalog) Remove(_ context.Context, entry vaultcatalog.Entry) error {
	c.removed = entry
	return nil
}
