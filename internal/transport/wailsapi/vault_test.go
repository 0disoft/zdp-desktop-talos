package wailsapi

import (
	"context"
	"errors"
	"testing"

	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/vault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultcatalog"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultdb"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
)

func TestVaultServiceCreatesThenLocksSession(t *testing.T) {
	t.Parallel()
	keys := &serviceKeyStore{}
	database := &serviceDatabase{}
	creator, err := vaultbootstrap.NewCreator(keys, &serviceDatabaseFactory{database: database}, &serviceCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	service := NewVaultService(creator, nil)

	created := service.Create(30, "correlation-create")
	if created.Error != nil || created.Vault == nil || created.Vault.State != "unlocked" || created.Vault.Revision != 1 {
		t.Fatalf("create result = %+v", created)
	}
	duplicate := service.Create(30, "correlation-duplicate")
	if duplicate.Error == nil || duplicate.Error.Code != "VAULT_ALREADY_OPEN" {
		t.Fatalf("duplicate result = %+v", duplicate)
	}
	locked := service.Lock("correlation-lock")
	if locked.Error != nil || locked.Vault == nil || locked.Vault.State != "locked" || !database.closed {
		t.Fatalf("lock result = %+v, database=%+v", locked, database)
	}
}

func TestVaultServiceListsAndReopensCreatedVault(t *testing.T) {
	t.Parallel()
	keys := &serviceKeyStore{}
	database := &serviceDatabase{}
	factory := &serviceDatabaseFactory{database: database}
	catalog := &serviceCatalog{}
	creator, _ := vaultbootstrap.NewCreator(keys, factory, catalog)
	service := NewVaultService(creator, nil)
	created := service.Create(90, "create")
	if created.Error != nil || created.Vault == nil {
		t.Fatalf("create = %+v", created)
	}
	vaultID := created.Vault.VaultID
	if result := service.Lock("lock"); result.Error != nil {
		t.Fatalf("lock = %+v", result)
	}
	listed := service.List("list")
	if listed.Error != nil || len(listed.Vaults) != 1 || listed.Vaults[0].VaultID != vaultID {
		t.Fatalf("list = %+v", listed)
	}
	reopened := service.Open(vaultID, "open")
	if reopened.Error != nil || reopened.Vault == nil || reopened.Vault.State != "unlocked" || reopened.Vault.RetentionDays != 90 {
		t.Fatalf("open = %+v", reopened)
	}
}

func TestVaultServiceUpdatesRetentionAndRejectsStaleRevision(t *testing.T) {
	t.Parallel()
	database := &serviceDatabase{}
	creator, _ := vaultbootstrap.NewCreator(&serviceKeyStore{}, &serviceDatabaseFactory{database: database}, &serviceCatalog{})
	service := NewVaultService(creator, nil)
	created := service.Create(30, "create")
	updated := service.UpdateRetention(90, created.Vault.Revision, "request-1", "update")
	if updated.Error != nil || updated.Vault == nil || updated.Vault.Revision != 2 || updated.Vault.RetentionDays != 90 {
		t.Fatalf("updated=%+v", updated)
	}
	stale := service.UpdateRetention(365, 1, "request-2", "stale")
	if stale.Error == nil || stale.Error.Code != "VAULT_REVISION_CONFLICT" {
		t.Fatalf("stale=%+v", stale)
	}
	if status := service.Status(); status.Revision != 2 || status.RetentionDays != 90 {
		t.Fatalf("status=%+v", status)
	}
}

func TestVaultServiceFailsClosedWhenStorageIsUnavailable(t *testing.T) {
	t.Parallel()
	service := NewVaultService(nil, errors.New("C:\\Users\\private\\keys"))
	result := service.Create(30, "correlation-unavailable")
	if result.Error == nil || result.Error.Code != "VAULT_STORAGE_UNAVAILABLE" {
		t.Fatalf("result = %+v", result)
	}
	if result.Error.Message == "" || result.Error.CorrelationID != "correlation-unavailable" {
		t.Fatalf("error = %+v", result.Error)
	}
}

func TestVaultServiceDoesNotReportOpenAfterCloseFailure(t *testing.T) {
	t.Parallel()
	database := &serviceDatabase{closeErr: errors.New("flush failed")}
	creator, _ := vaultbootstrap.NewCreator(&serviceKeyStore{}, &serviceDatabaseFactory{database: database}, &serviceCatalog{})
	service := NewVaultService(creator, nil)
	if result := service.Create(30, "create"); result.Error != nil {
		t.Fatalf("create result = %+v", result)
	}
	result := service.Lock("lock")
	if result.Error == nil || result.Error.Code != "VAULT_LOCK_FAILED" {
		t.Fatalf("lock result = %+v", result)
	}
	if status := service.Status(); status.State != "locked" {
		t.Fatalf("status after close failure = %+v", status)
	}
}

func TestMapErrorPrioritizesIncompleteCleanup(t *testing.T) {
	t.Parallel()
	mapped := MapError(errors.Join(vaultbootstrap.ErrCompensationFailed, vaultbootstrap.ErrInvalidInput), "cleanup")
	if mapped.Code != "VAULT_CLEANUP_INCOMPLETE" {
		t.Fatalf("mapped = %+v", mapped)
	}
}

type serviceKeyStore struct {
	value   []byte
	present bool
}

func (s *serviceKeyStore) Put(_ context.Context, _ keyvault.Reference, value []byte) error {
	s.value = append([]byte(nil), value...)
	s.present = true
	return nil
}
func (s *serviceKeyStore) Get(context.Context, keyvault.Reference) ([]byte, error) {
	if !s.present {
		return nil, keyvault.ErrNotFound
	}
	return append([]byte(nil), s.value...), nil
}
func (*serviceKeyStore) Rotate(context.Context, keyvault.Reference, []byte) error { return nil }
func (s *serviceKeyStore) Delete(context.Context, keyvault.Reference) error {
	s.present = false
	s.value = nil
	return nil
}

type serviceDatabaseFactory struct{ database vaultdb.Database }

func (f *serviceDatabaseFactory) Create(context.Context, string, string, []byte) (vaultdb.Database, error) {
	return f.database, nil
}
func (f *serviceDatabaseFactory) Open(context.Context, string, string, []byte) (vaultdb.Database, error) {
	if database, ok := f.database.(*serviceDatabase); ok {
		database.closed = false
	}
	return f.database, nil
}
func (*serviceDatabaseFactory) Remove(context.Context, string) error { return nil }

type serviceDatabase struct {
	closed   bool
	closeErr error
	record   vault.Record
}

func (d *serviceDatabase) CreateVault(_ context.Context, input vaultstore.CreateInput) (vault.Record, error) {
	d.record = vault.Record{
		ID: input.VaultID, Revision: 1, Status: vault.StatusActive, RetentionDays: input.RetentionDays,
		CreatedAt: input.OccurredAt, UpdatedAt: input.OccurredAt, LastEventID: "event-1",
	}
	return d.record, nil
}
func (d *serviceDatabase) GetVault(context.Context, string) (vault.Record, error) {
	return d.record, nil
}
func (d *serviceDatabase) UpdateVaultRetention(_ context.Context, input vaultstore.UpdateRetentionInput) (vault.Record, error) {
	if input.ExpectedRevision != d.record.Revision {
		return vault.Record{}, vaultstore.ErrRevisionConflict
	}
	d.record.Revision++
	d.record.RetentionDays = input.RetentionDays
	d.record.UpdatedAt = input.OccurredAt
	d.record.LastEventID = "event-retention"
	return d.record, nil
}
func (d *serviceDatabase) Close() error { d.closed = true; return d.closeErr }

type serviceCatalog struct{ entries []vaultcatalog.Entry }

func (c *serviceCatalog) List(context.Context) ([]vaultcatalog.Entry, error) { return c.entries, nil }
func (c *serviceCatalog) Add(_ context.Context, entry vaultcatalog.Entry) error {
	c.entries = append(c.entries, entry)
	return nil
}
func (*serviceCatalog) Remove(context.Context, vaultcatalog.Entry) error { return nil }
