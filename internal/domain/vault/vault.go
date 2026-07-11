package vault

import (
	"errors"
	"fmt"
	"time"
)

const (
	MinRetentionDays = 1
	MaxRetentionDays = 3650
)

type Status string

const (
	StatusActive Status = "active"
	StatusPurged Status = "purged"
)

var ErrInvalidRecord = errors.New("invalid vault record")

type Record struct {
	ID            string
	Revision      int
	Status        Status
	RetentionDays int
	CreatedAt     time.Time
	UpdatedAt     time.Time
	LastEventID   string
}

func (r Record) Validate() error {
	if r.ID == "" || r.LastEventID == "" {
		return fmt.Errorf("%w: id and last event id are required", ErrInvalidRecord)
	}
	if r.Revision < 1 {
		return fmt.Errorf("%w: revision must be positive", ErrInvalidRecord)
	}
	if r.Status != StatusActive && r.Status != StatusPurged {
		return fmt.Errorf("%w: unknown status %q", ErrInvalidRecord, r.Status)
	}
	if err := ValidateRetentionDays(r.RetentionDays); err != nil {
		return err
	}
	if r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) {
		return fmt.Errorf("%w: timestamps are missing or reversed", ErrInvalidRecord)
	}
	return nil
}

func ValidateRetentionDays(days int) error {
	if days < MinRetentionDays || days > MaxRetentionDays {
		return fmt.Errorf("%w: retention days must be between %d and %d", ErrInvalidRecord, MinRetentionDays, MaxRetentionDays)
	}
	return nil
}
