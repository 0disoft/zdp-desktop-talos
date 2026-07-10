package keyvault

import (
	"context"
	"errors"
)

var (
	ErrUnsupported      = errors.New("key vault is unsupported on this platform")
	ErrInvalidReference = errors.New("key vault reference is invalid")
	ErrInvalidSecret    = errors.New("key vault secret is invalid")
	ErrNotFound         = errors.New("key vault secret was not found")
	ErrAlreadyExists    = errors.New("key vault secret already exists")
	ErrCorrupt          = errors.New("key vault record is corrupt")
	ErrUnseal           = errors.New("key vault record cannot be unsealed")
)

type Reference struct {
	VaultID string
	KeyID   string
}

type Store interface {
	Put(ctx context.Context, ref Reference, secret []byte) error
	Get(ctx context.Context, ref Reference) ([]byte, error)
	Rotate(ctx context.Context, ref Reference, secret []byte) error
	Delete(ctx context.Context, ref Reference) error
}
