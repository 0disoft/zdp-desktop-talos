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

type State string

const (
	StateActive       State = "active"
	StatePurgePending State = "purge_pending"
)

type Entry struct {
	VaultID   string
	CreatedAt time.Time
	State     State
}

type Catalog interface {
	List(context.Context) ([]Entry, error)
	PendingPurges(context.Context) ([]Entry, error)
	Add(context.Context, Entry) error
	MarkPurgePending(context.Context, Entry) error
	Remove(context.Context, Entry) error
}
