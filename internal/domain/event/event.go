package event

import (
	"errors"
	"fmt"
	"time"
)

type Sensitivity string

const (
	SensitivityPublic    Sensitivity = "public"
	SensitivityPrivate   Sensitivity = "private"
	SensitivitySensitive Sensitivity = "sensitive"
	SensitivitySecret    Sensitivity = "secret"
)

var (
	ErrInvalidEvent  = errors.New("invalid event")
	ErrSecretPayload = errors.New("secret payload persistence is forbidden")
)

type Record struct {
	ID            string
	VaultID       string
	Type          string
	SchemaVersion int
	Sensitivity   Sensitivity
	Payload       []byte
	OccurredAt    time.Time
}

func (r Record) Validate() error {
	if r.ID == "" || r.VaultID == "" || r.Type == "" {
		return fmt.Errorf("%w: id, vault id, and type are required", ErrInvalidEvent)
	}
	if r.SchemaVersion < 1 {
		return fmt.Errorf("%w: schema version must be positive", ErrInvalidEvent)
	}
	if r.OccurredAt.IsZero() {
		return fmt.Errorf("%w: occurred_at is required", ErrInvalidEvent)
	}
	if err := r.Sensitivity.Validate(); err != nil {
		return err
	}
	if r.Sensitivity == SensitivitySecret && len(r.Payload) > 0 {
		return ErrSecretPayload
	}
	return nil
}

func (s Sensitivity) Validate() error {
	switch s {
	case SensitivityPublic, SensitivityPrivate, SensitivitySensitive, SensitivitySecret:
		return nil
	default:
		return fmt.Errorf("%w: unknown sensitivity %q", ErrInvalidEvent, s)
	}
}
