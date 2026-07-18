package enrollmentstore

import (
	"context"
	"errors"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/syncenrollment"
)

var (
	ErrInvalidCommand      = errors.New("invalid sync enrollment command")
	ErrNotFound            = errors.New("sync enrollment was not found")
	ErrConflict            = errors.New("sync enrollment conflicts with stored state")
	ErrResponseUnavailable = errors.New("sync enrollment response is unavailable")
)

type RecordOfferInput struct {
	EnrollmentID string
	VaultID      string
	OfferHash    string
	ExpiresAt    time.Time
	OccurredAt   time.Time
}

type RecordAcceptanceInput struct {
	EnrollmentID      string
	VaultID           string
	SourceDeviceID    string
	OfferHash         string
	AcceptanceHash    string
	EncodedAcceptance []byte
	ExpiresAt         time.Time
	OccurredAt        time.Time
}

type CompleteInput struct {
	EnrollmentID   string
	VaultID        string
	TargetDeviceID string
	OfferHash      string
	AcceptanceHash string
	OccurredAt     time.Time
}

type CancelInput struct {
	EnrollmentID string
	VaultID      string
	OccurredAt   time.Time
}

type Store interface {
	RecordEnrollmentOffer(context.Context, RecordOfferInput) (syncenrollment.Record, bool, error)
	RecordEnrollmentAcceptance(context.Context, RecordAcceptanceInput) (syncenrollment.Record, []byte, bool, error)
	CompleteEnrollment(context.Context, CompleteInput) (syncenrollment.Record, bool, error)
	CancelEnrollment(context.Context, CancelInput) (syncenrollment.Record, bool, error)
	ExpireEnrollments(context.Context, string, time.Time) ([]syncenrollment.Record, error)
	GetEnrollment(context.Context, string, string) (syncenrollment.Record, []byte, error)
	ListEnrollments(context.Context, string, int) ([]syncenrollment.Record, error)
}
