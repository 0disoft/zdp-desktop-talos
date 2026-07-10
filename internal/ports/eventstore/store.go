package eventstore

import (
	"context"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
)

type AppendInput struct {
	VaultID        string
	Type           string
	SchemaVersion  int
	Sensitivity    event.Sensitivity
	Payload        []byte
	OccurredAt     time.Time
	IdempotencyKey string
}

type Store interface {
	Append(context.Context, AppendInput) (event.Record, error)
	Get(context.Context, string) (event.Record, error)
	Close() error
}
