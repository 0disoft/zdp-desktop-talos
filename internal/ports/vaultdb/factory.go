package vaultdb

import (
	"context"
	"errors"

	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
)

var (
	ErrAlreadyExists = errors.New("vault database already exists")
	ErrNotFound      = errors.New("vault database was not found")
)

type Database interface {
	vaultstore.Store
	Close() error
}

type Factory interface {
	Create(context.Context, string, string, []byte) (Database, error)
	Remove(context.Context, string) error
}
