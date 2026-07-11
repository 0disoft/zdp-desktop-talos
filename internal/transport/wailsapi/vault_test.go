package wailsapi

import (
	"context"
	"errors"
	"testing"

	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/vault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultdb"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
)

func TestVaultServiceCreatesThenLocksSession(t *testing.T) {
	t.Parallel()
	keys := &serviceKeyStore{}
	database := &serviceDatabase{}
	creator, err := vaultbootstrap.NewCreator(keys, &serviceDatabaseFactory{database: database})
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
	creator, _ := vaultbootstrap.NewCreator(&serviceKeyStore{}, &serviceDatabaseFactory{database: database})
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

type serviceKeyStore struct{}

func (*serviceKeyStore) Put(context.Context, keyvault.Reference, []byte) error { return nil }
func (*serviceKeyStore) Get(context.Context, keyvault.Reference) ([]byte, error) {
	return nil, keyvault.ErrNotFound
}
func (*serviceKeyStore) Rotate(context.Context, keyvault.Reference, []byte) error { return nil }
func (*serviceKeyStore) Delete(context.Context, keyvault.Reference) error         { return nil }

type serviceDatabaseFactory struct{ database vaultdb.Database }

func (f *serviceDatabaseFactory) Create(context.Context, string, string, []byte) (vaultdb.Database, error) {
	return f.database, nil
}
func (*serviceDatabaseFactory) Remove(context.Context, string) error { return nil }

type serviceDatabase struct {
	closed   bool
	closeErr error
}

func (*serviceDatabase) CreateVault(_ context.Context, input vaultstore.CreateInput) (vault.Record, error) {
	return vault.Record{
		ID: input.VaultID, Revision: 1, Status: vault.StatusActive, RetentionDays: input.RetentionDays,
		CreatedAt: input.OccurredAt, UpdatedAt: input.OccurredAt, LastEventID: "event-1",
	}, nil
}
func (*serviceDatabase) GetVault(context.Context, string) (vault.Record, error) {
	return vault.Record{}, nil
}
func (*serviceDatabase) UpdateVaultRetention(context.Context, vaultstore.UpdateRetentionInput) (vault.Record, error) {
	return vault.Record{}, nil
}
func (d *serviceDatabase) Close() error { d.closed = true; return d.closeErr }
