package syncstore

import (
	"context"
	"crypto/ed25519"
	"errors"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
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
	ErrNoExportableEvents  = errors.New("no exportable events are available")
	ErrExportConflict      = errors.New("sync export batch conflicts with stored state")
	ErrReplayConflict      = errors.New("sync replay conflicts with validated pack state")
	ErrReplayNotFound      = errors.New("sync replay was not found")
)

type RegisterDeviceInput struct {
	VaultID        string
	DeviceID       string
	PublicKey      ed25519.PublicKey
	OccurredAt     time.Time
	IdempotencyKey string
}

type RevokeDeviceInput struct {
	VaultID           string
	AuthorityDeviceID string
	DeviceID          string
	ExpectedRevision  int
	OccurredAt        time.Time
	IdempotencyKey    string
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

type PrepareExportInput struct {
	VaultID    string
	DeviceID   string
	Limit      int
	OccurredAt time.Time
}

type PreparedExport struct {
	Batch  syncstate.ExportBatch
	Events []event.Record
}

type FinalizeExportInput struct {
	ExportID       string
	VaultID        string
	DeviceID       string
	PackID         string
	SequenceStart  uint64
	SequenceEnd    uint64
	EventCount     int
	CiphertextHash string
	EncodedPack    []byte
	OccurredAt     time.Time
}

type ReplayEvent struct {
	DeviceSeq uint64
	Record    event.Record
}

type ApplyReplayInput struct {
	PackID      string
	VaultID     string
	DeviceID    string
	Events      []ReplayEvent
	CompletedAt time.Time
}

type Store interface {
	RegisterSyncDevice(context.Context, RegisterDeviceInput) (syncstate.Device, error)
	RevokeSyncDevice(context.Context, RevokeDeviceInput) (syncstate.Device, error)
	GetSyncDevice(context.Context, string, string) (syncstate.Device, error)
	ListSyncDevices(context.Context, string, int) ([]syncstate.Device, error)
	RecordValidatedSyncPack(context.Context, RecordPackInput) (syncstate.PackReceipt, bool, error)
	GetValidatedSyncPack(context.Context, string, string) (syncstate.PackReceipt, []byte, error)
	PrepareSyncExport(context.Context, PrepareExportInput) (PreparedExport, bool, error)
	FinalizeSyncExport(context.Context, FinalizeExportInput) (syncstate.ExportBatch, []byte, bool, error)
	GetSyncExport(context.Context, string, string) (syncstate.ExportBatch, []byte, error)
}

type ReplayStore interface {
	ApplyValidatedSyncPack(context.Context, ApplyReplayInput) (syncstate.ReplayResult, bool, error)
	GetSyncReplay(context.Context, string, string) (syncstate.ReplayResult, error)
	ListSyncReplays(context.Context, string, int) ([]syncstate.ReplayResult, error)
}
