package vaultdb

import (
	"context"
	"errors"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/verification"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/accountstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/artifactstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/decisionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/executionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/patchstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
)

var (
	ErrAlreadyExists = errors.New("vault database already exists")
	ErrNotFound      = errors.New("vault database was not found")
)

type Database interface {
	accountstore.Store
	vaultstore.Store
	artifactstore.Store
	decisionstore.Store
	executionstore.Store
	patchstore.Store
	taskstore.Store
	GetLatestVerificationEvidence(context.Context, string, string) (verification.Evidence, error)
	Close() error
}

type Factory interface {
	Create(context.Context, string, string, []byte) (Database, error)
	Open(context.Context, string, string, []byte) (Database, error)
	Remove(context.Context, string) error
	Purge(context.Context, string) error
}
