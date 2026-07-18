package syncstore

import (
	"context"
	"crypto/ed25519"
	"errors"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/syncstate"
)

var (
	ErrInvalidCommand      = errors.New("invalid sync store command")
	ErrDeviceNotFound      = errors.New("sync device was not found")
	ErrDeviceExists        = errors.New("sync device already exists")
	ErrDeviceRevoked       = errors.New("sync device is revoked")
	ErrRevisionConflict    = errors.New("sync device revision conflict")
	ErrSequenceConflict    = errors.New("sync pack sequence conflicts with the device cursor")
	ErrPackConflict        = errors.New("sync pack identity conflicts with a stored receipt")
	ErrIdempotencyConflict = errors.New("sync command idempotency conflict")
)

type RegisterDeviceInput struct {
	VaultID        string
	DeviceID       string
	PublicKey      ed25519.PublicKey
	OccurredAt     time.Time
	IdempotencyKey string
}

type RevokeDeviceInput struct {
	VaultID          string
	DeviceID         string
	ExpectedRevision int
	OccurredAt       time.Time
	IdempotencyKey   string
}

type RecordPackInput struct {
	PackID         string
	VaultID        string
	DeviceID       string
	SequenceStart  uint64
	SequenceEnd    uint64
	EventCount     int
	CiphertextHash string
	EncodedPack    []byte
	ReceivedAt     time.Time
}

type Store interface {
	RegisterSyncDevice(context.Context, RegisterDeviceInput) (syncstate.Device, error)
	RevokeSyncDevice(context.Context, RevokeDeviceInput) (syncstate.Device, error)
	GetSyncDevice(context.Context, string, string) (syncstate.Device, error)
	RecordValidatedSyncPack(context.Context, RecordPackInput) (syncstate.PackReceipt, bool, error)
	GetValidatedSyncPack(context.Context, string, string) (syncstate.PackReceipt, []byte, error)
}
