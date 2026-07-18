package syncstate

import (
	"strings"
	"time"
)

type ExportState string

const (
	ExportPreparing ExportState = "preparing"
	ExportReady     ExportState = "ready"
)

type ExportBatch struct {
	ExportID       string
	VaultID        string
	DeviceID       string
	SequenceStart  uint64
	SequenceEnd    uint64
	EventCount     int
	State          ExportState
	PackID         string
	CiphertextHash string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (b ExportBatch) Validate() error {
	if strings.TrimSpace(b.ExportID) == "" || len(b.ExportID) > 128 || strings.TrimSpace(b.VaultID) == "" || strings.TrimSpace(b.DeviceID) == "" || len(b.DeviceID) > MaxDeviceIDLength || b.SequenceStart < 1 || b.SequenceEnd < b.SequenceStart || uint64(b.EventCount) != b.SequenceEnd-b.SequenceStart+1 || b.EventCount > MaxEventsPerPack || b.CreatedAt.IsZero() || b.UpdatedAt.Before(b.CreatedAt) {
		return ErrInvalidRecord
	}
	switch b.State {
	case ExportPreparing:
		if b.PackID != "" || b.CiphertextHash != "" {
			return ErrInvalidRecord
		}
	case ExportReady:
		if !validSHA256Reference(b.PackID) || !validHexSHA256(b.CiphertextHash) {
			return ErrInvalidRecord
		}
	default:
		return ErrInvalidRecord
	}
	return nil
}
