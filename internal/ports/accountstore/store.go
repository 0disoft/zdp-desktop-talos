package accountstore

import (
	"context"
	"errors"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/accountlink"
)

var (
	ErrInvalidCommand        = errors.New("invalid account link command")
	ErrNotFound              = errors.New("account link was not found")
	ErrRevisionConflict      = errors.New("account link revision conflict")
	ErrIdempotencyConflict   = errors.New("account link idempotency conflict")
	ErrIdempotencyUnverified = errors.New("account link idempotency result cannot be verified")
)

type LinkInput struct {
	VaultID          string
	ExpectedRevision int
	Identity         accountlink.VerifiedIdentity
	OccurredAt       time.Time
	IdempotencyKey   string
}

type UnlinkInput struct {
	VaultID          string
	ExpectedRevision int
	OccurredAt       time.Time
	IdempotencyKey   string
}

type Store interface {
	LinkAccount(context.Context, LinkInput) (accountlink.Record, error)
	UnlinkAccount(context.Context, UnlinkInput) (accountlink.Record, error)
	GetAccountLink(context.Context, string) (accountlink.Record, error)
}
