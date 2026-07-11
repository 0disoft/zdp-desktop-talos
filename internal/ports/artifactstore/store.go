package artifactstore

import (
	"context"
	"errors"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/artifact"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
)

var (
	ErrNotFound     = errors.New("artifact was not found")
	ErrInvalidInput = errors.New("artifact input is invalid")
	ErrCorrupt      = errors.New("artifact ciphertext or metadata is corrupt")
)

type PutInput struct {
	VaultID       string
	SchemaVersion int
	Sensitivity   event.Sensitivity
	ContentType   string
	Payload       []byte
}

type Store interface {
	PutArtifact(context.Context, PutInput) (artifact.Record, error)
	GetArtifact(context.Context, string) (artifact.Record, []byte, error)
	ReconcileArtifacts(context.Context) error
}
