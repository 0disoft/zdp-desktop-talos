package vaultbootstrap

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/artifact"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/vault"
	"github.com/0disoft/zdp-desktop-talos/internal/id"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/artifactstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultcatalog"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultdb"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
)

const (
	VaultKeyID    = "vault-kek-v1"
	vaultKeyBytes = 32
)

var (
	ErrInvalidInput       = errors.New("invalid Vault creation input")
	ErrCompensationFailed = errors.New("Vault creation failed and cleanup was incomplete")
	ErrNotCataloged       = errors.New("Vault is not present in the protected catalog")
	ErrNotOpen            = errors.New("Vault session is not open")
)

type CreateInput struct {
	RetentionDays int
}

type UpdateRetentionInput struct {
	ExpectedRevision int
	RetentionDays    int
	IdempotencyKey   string
}

type StoreArtifactInput struct {
	SchemaVersion int
	Sensitivity   event.Sensitivity
	ContentType   string
	Payload       []byte
}

type Session struct {
	Record   vault.Record
	database vaultdb.Database
}

func (s *Session) Close() error {
	if s == nil || s.database == nil {
		return nil
	}
	err := s.database.Close()
	s.database = nil
	return err
}

func (s *Session) UpdateRetention(ctx context.Context, input UpdateRetentionInput) (vault.Record, error) {
	if s == nil || s.database == nil {
		return vault.Record{}, ErrNotOpen
	}
	if input.ExpectedRevision < 1 || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 {
		return vault.Record{}, ErrInvalidInput
	}
	if err := vault.ValidateRetentionDays(input.RetentionDays); err != nil {
		return vault.Record{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if input.ExpectedRevision != s.Record.Revision {
		return vault.Record{}, vaultstore.ErrRevisionConflict
	}
	record, err := s.database.UpdateVaultRetention(ctx, vaultstore.UpdateRetentionInput{
		VaultID:          s.Record.ID,
		ExpectedRevision: input.ExpectedRevision,
		RetentionDays:    input.RetentionDays,
		OccurredAt:       time.Now().UTC(),
		IdempotencyKey:   input.IdempotencyKey,
	})
	if err != nil {
		return vault.Record{}, err
	}
	s.Record = record
	return record, nil
}

func (s *Session) StoreArtifact(ctx context.Context, input StoreArtifactInput) (artifact.Record, error) {
	if s == nil || s.database == nil {
		return artifact.Record{}, ErrNotOpen
	}
	return s.database.PutArtifact(ctx, artifactstore.PutInput{
		VaultID: s.Record.ID, SchemaVersion: input.SchemaVersion, Sensitivity: input.Sensitivity,
		ContentType: input.ContentType, Payload: input.Payload,
	})
}

func (s *Session) LoadArtifact(ctx context.Context, artifactID string) (artifact.Record, []byte, error) {
	if s == nil || s.database == nil {
		return artifact.Record{}, nil, ErrNotOpen
	}
	record, payload, err := s.database.GetArtifact(ctx, artifactID)
	if err != nil {
		return artifact.Record{}, nil, err
	}
	if record.VaultID != s.Record.ID {
		clear(payload)
		return artifact.Record{}, nil, artifactstore.ErrNotFound
	}
	return record, payload, nil
}

type Creator struct {
	keys      keyvault.Store
	databases vaultdb.Factory
	catalog   vaultcatalog.Catalog
	random    io.Reader
	now       func() time.Time
}

func NewCreator(keys keyvault.Store, databases vaultdb.Factory, catalog vaultcatalog.Catalog) (*Creator, error) {
	if keys == nil || databases == nil || catalog == nil {
		return nil, ErrInvalidInput
	}
	return &Creator{
		keys:      keys,
		databases: databases,
		catalog:   catalog,
		random:    rand.Reader,
		now:       func() time.Time { return time.Now().UTC() },
	}, nil
}

func (c *Creator) Create(ctx context.Context, input CreateInput) (*Session, error) {
	if err := vault.ValidateRetentionDays(input.RetentionDays); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	now := c.now().UTC()
	vaultID, err := id.UUIDv7(now, c.random)
	if err != nil {
		return nil, fmt.Errorf("generate Vault ID: %w", err)
	}
	key := make([]byte, vaultKeyBytes)
	if _, err := io.ReadFull(c.random, key); err != nil {
		return nil, fmt.Errorf("generate Vault key: %w", err)
	}
	defer clear(key)

	keyRef := keyvault.Reference{VaultID: vaultID, KeyID: VaultKeyID}
	if err := c.keys.Put(ctx, keyRef, key); err != nil {
		return nil, fmt.Errorf("persist Vault key: %w", err)
	}

	database, err := c.databases.Create(ctx, vaultID, VaultKeyID, key)
	if err != nil {
		return nil, c.compensate(ctx, fmt.Errorf("create Vault database: %w", err), vaultcatalog.Entry{}, keyRef, nil)
	}
	record, err := database.CreateVault(ctx, vaultstore.CreateInput{
		VaultID:        vaultID,
		RetentionDays:  input.RetentionDays,
		OccurredAt:     now,
		IdempotencyKey: "vault-bootstrap:" + vaultID,
	})
	if err != nil {
		return nil, c.compensate(ctx, fmt.Errorf("initialize Vault state: %w", err), vaultcatalog.Entry{}, keyRef, database)
	}
	entry := vaultcatalog.Entry{VaultID: vaultID, CreatedAt: record.CreatedAt}
	if err := c.catalog.Add(ctx, entry); err != nil {
		return nil, c.compensate(ctx, fmt.Errorf("register Vault in catalog: %w", err), entry, keyRef, database)
	}
	return &Session{Record: record, database: database}, nil
}

func (c *Creator) List(ctx context.Context) ([]vaultcatalog.Entry, error) {
	return c.catalog.List(ctx)
}

func (c *Creator) Open(ctx context.Context, vaultID string) (*Session, error) {
	if !id.IsUUIDv7(vaultID) {
		return nil, ErrNotCataloged
	}
	entries, err := c.catalog.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("read protected Vault catalog: %w", err)
	}
	found := false
	for _, entry := range entries {
		if entry.VaultID == vaultID {
			found = true
			break
		}
	}
	if !found {
		return nil, ErrNotCataloged
	}
	keyRef := keyvault.Reference{VaultID: vaultID, KeyID: VaultKeyID}
	key, err := c.keys.Get(ctx, keyRef)
	if err != nil {
		return nil, fmt.Errorf("load Vault key: %w", err)
	}
	defer clear(key)
	database, err := c.databases.Open(ctx, vaultID, VaultKeyID, key)
	if err != nil {
		return nil, fmt.Errorf("open Vault database: %w", err)
	}
	record, err := database.GetVault(ctx, vaultID)
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("load Vault state: %w", err)
	}
	if record.ID != vaultID || record.Status != vault.StatusActive {
		_ = database.Close()
		return nil, fmt.Errorf("%w: stored Vault identity or state is invalid", ErrNotCataloged)
	}
	return &Session{Record: record, database: database}, nil
}

func (c *Creator) compensate(ctx context.Context, cause error, entry vaultcatalog.Entry, keyRef keyvault.Reference, database vaultdb.Database) error {
	cleanupCtx := context.WithoutCancel(ctx)
	var cleanupErrors []error
	if !entry.CreatedAt.IsZero() {
		if err := c.catalog.Remove(cleanupCtx, entry); err != nil && !errors.Is(err, vaultcatalog.ErrNotFound) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove partial Vault catalog entry: %w", err))
		}
	}
	if database != nil {
		if err := database.Close(); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("close partial Vault database: %w", err))
		}
	}
	if err := c.databases.Remove(cleanupCtx, keyRef.VaultID); err != nil && !errors.Is(err, vaultdb.ErrNotFound) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("remove partial Vault database: %w", err))
	}
	if err := c.keys.Delete(cleanupCtx, keyRef); err != nil && !errors.Is(err, keyvault.ErrNotFound) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("remove partial Vault key: %w", err))
	}
	if len(cleanupErrors) == 0 {
		return cause
	}
	return errors.Join(ErrCompensationFailed, cause, errors.Join(cleanupErrors...))
}
