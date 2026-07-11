package vaultstore

import (
	"context"
	"errors"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/vault"
)

var (
	ErrNotFound              = errors.New("vault was not found")
	ErrAlreadyExists         = errors.New("vault already exists")
	ErrRevisionConflict      = errors.New("vault revision conflict")
	ErrInvalidCommand        = errors.New("invalid vault command")
	ErrIdempotencyConflict   = errors.New("vault idempotency key was used for a different command")
	ErrIdempotencyUnverified = errors.New("legacy vault idempotency result cannot be verified")
)

type CreateInput struct {
	VaultID        string
	RetentionDays  int
	OccurredAt     time.Time
	IdempotencyKey string
}

type UpdateRetentionInput struct {
	VaultID          string
	ExpectedRevision int
	RetentionDays    int
	OccurredAt       time.Time
	IdempotencyKey   string
}

type Store interface {
	CreateVault(context.Context, CreateInput) (vault.Record, error)
	GetVault(context.Context, string) (vault.Record, error)
	UpdateVaultRetention(context.Context, UpdateRetentionInput) (vault.Record, error)
}
