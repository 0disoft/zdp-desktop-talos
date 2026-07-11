package vaultcatalog

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("Vault catalog entry was not found")
	ErrConflict = errors.New("Vault catalog entry already exists")
	ErrCorrupt  = errors.New("Vault catalog is corrupt")
)

type Entry struct {
	VaultID   string
	CreatedAt time.Time
}

type Catalog interface {
	List(context.Context) ([]Entry, error)
	Add(context.Context, Entry) error
	Remove(context.Context, Entry) error
}
