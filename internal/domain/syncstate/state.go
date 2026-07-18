package syncstate

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"time"
)

const (
	MaxDeviceIDLength = 128
	MaxEventsPerPack  = 512
	MaxPackBytes      = 16 << 20
)

var ErrInvalidRecord = errors.New("invalid sync state record")

type DeviceState string

const (
	DeviceActive  DeviceState = "active"
	DeviceRevoked DeviceState = "revoked"
)

type Device struct {
	VaultID      string
	DeviceID     string
	PublicKey    ed25519.PublicKey
	State        DeviceState
	Revision     int
	NextSequence uint64
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastEventID  string
}

func (d Device) Validate() error {
	if strings.TrimSpace(d.VaultID) == "" || strings.TrimSpace(d.DeviceID) == "" || len(d.DeviceID) > MaxDeviceIDLength || len(d.PublicKey) != ed25519.PublicKeySize || d.Revision < 1 || d.NextSequence < 1 || d.CreatedAt.IsZero() || d.UpdatedAt.Before(d.CreatedAt) || d.LastEventID == "" {
		return ErrInvalidRecord
	}
	if d.State != DeviceActive && d.State != DeviceRevoked {
		return ErrInvalidRecord
	}
	return nil
}

type PackState string

const PackValidated PackState = "validated"

type PackReceipt struct {
	PackID         string
	VaultID        string
	DeviceID       string
	SequenceStart  uint64
	SequenceEnd    uint64
	EventCount     int
	CiphertextHash string
	State          PackState
	ReceivedAt     time.Time
	LastEventID    string
}

func (r PackReceipt) Validate() error {
	if !validSHA256Reference(r.PackID) || strings.TrimSpace(r.VaultID) == "" || strings.TrimSpace(r.DeviceID) == "" || len(r.DeviceID) > MaxDeviceIDLength || r.SequenceStart < 1 || r.SequenceEnd < r.SequenceStart || r.SequenceEnd >= math.MaxInt64 || uint64(r.EventCount) != r.SequenceEnd-r.SequenceStart+1 || r.EventCount > MaxEventsPerPack || !validHexSHA256(r.CiphertextHash) || r.State != PackValidated || r.ReceivedAt.IsZero() || r.LastEventID == "" {
		return ErrInvalidRecord
	}
	return nil
}

func validSHA256Reference(value string) bool {
	return strings.HasPrefix(value, "sha256:") && validHexSHA256(strings.TrimPrefix(value, "sha256:"))
}

func validHexSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == value
}
