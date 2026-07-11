package artifact

import (
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
)

const MaxPayloadBytes = 64 << 20

var ErrInvalidRecord = errors.New("invalid artifact record")

type Record struct {
	ID             string
	VaultID        string
	SchemaVersion  int
	Sensitivity    event.Sensitivity
	ContentType    string
	SizeBytes      int64
	ContentHash    string
	CiphertextHash string
	CreatedAt      time.Time
}

func (r Record) Validate() error {
	if r.ID == "" || r.VaultID == "" || r.ContentType == "" || r.ContentHash == "" || r.CiphertextHash == "" {
		return fmt.Errorf("%w: identifiers, hashes, and content type are required", ErrInvalidRecord)
	}
	if !validSHA256(r.ContentHash) || !validSHA256(r.CiphertextHash) {
		return fmt.Errorf("%w: hashes must be lowercase SHA-256", ErrInvalidRecord)
	}
	if r.SchemaVersion < 1 || r.SizeBytes < 1 || r.SizeBytes > MaxPayloadBytes {
		return fmt.Errorf("%w: schema version or size is invalid", ErrInvalidRecord)
	}
	if r.Sensitivity == event.SensitivitySecret || r.Sensitivity.Validate() != nil {
		return fmt.Errorf("%w: sensitivity is invalid for persisted artifacts", ErrInvalidRecord)
	}
	if r.CreatedAt.IsZero() {
		return fmt.Errorf("%w: creation time is required", ErrInvalidRecord)
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
