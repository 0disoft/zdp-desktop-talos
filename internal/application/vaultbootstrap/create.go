package vaultbootstrap

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/vault"
	"github.com/0disoft/zdp-desktop-talos/internal/id"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
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
)

type CreateInput struct {
	RetentionDays int
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

type Creator struct {
	keys      keyvault.Store
	databases vaultdb.Factory
	random    io.Reader
	now       func() time.Time
}

func NewCreator(keys keyvault.Store, databases vaultdb.Factory) (*Creator, error) {
	if keys == nil || databases == nil {
		return nil, ErrInvalidInput
	}
	return &Creator{
		keys:      keys,
		databases: databases,
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
		return nil, c.compensate(ctx, fmt.Errorf("create Vault database: %w", err), vaultID, keyRef, nil)
	}
	record, err := database.CreateVault(ctx, vaultstore.CreateInput{
		VaultID:        vaultID,
		RetentionDays:  input.RetentionDays,
		OccurredAt:     now,
		IdempotencyKey: "vault-bootstrap:" + vaultID,
	})
	if err != nil {
		return nil, c.compensate(ctx, fmt.Errorf("initialize Vault state: %w", err), vaultID, keyRef, database)
	}
	return &Session{Record: record, database: database}, nil
}

func (c *Creator) compensate(ctx context.Context, cause error, vaultID string, keyRef keyvault.Reference, database vaultdb.Database) error {
	cleanupCtx := context.WithoutCancel(ctx)
	var cleanupErrors []error
	if database != nil {
		if err := database.Close(); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("close partial Vault database: %w", err))
		}
	}
	if err := c.databases.Remove(cleanupCtx, vaultID); err != nil && !errors.Is(err, vaultdb.ErrNotFound) {
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
