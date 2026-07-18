package syncenrollment

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const (
	MaxPackageBytes      = 64 << 10
	MaxEnrollmentIDBytes = 96
	MaxDeviceIDBytes     = 128
	SecretBytes          = 32
	MinValidity          = 5 * time.Minute
	MaxValidity          = 24 * time.Hour
	MaxClockSkew         = 5 * time.Minute
)

var ErrInvalidRecord = errors.New("invalid sync enrollment record")

type Role string

const (
	RoleIssuer    Role = "issuer"
	RoleRecipient Role = "recipient"
)

type State string

const (
	StateOffered   State = "offered"
	StateAccepted  State = "accepted"
	StateCompleted State = "completed"
)

type Record struct {
	EnrollmentID   string
	VaultID        string
	Role           Role
	State          State
	PeerDeviceID   string
	OfferHash      string
	AcceptanceHash string
	ExpiresAt      time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	CreatedEventID string
	LastEventID    string
}

func (r Record) Validate() error {
	if strings.TrimSpace(r.EnrollmentID) == "" || len(r.EnrollmentID) > MaxEnrollmentIDBytes || strings.TrimSpace(r.VaultID) == "" || !validHash(r.OfferHash) || r.ExpiresAt.IsZero() || r.CreatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) || r.ExpiresAt.Before(r.CreatedAt) || r.CreatedEventID == "" || r.LastEventID == "" {
		return ErrInvalidRecord
	}
	switch {
	case r.Role == RoleIssuer && r.State == StateOffered:
		if r.PeerDeviceID != "" || r.AcceptanceHash != "" {
			return ErrInvalidRecord
		}
	case r.Role == RoleIssuer && r.State == StateCompleted:
		if strings.TrimSpace(r.PeerDeviceID) == "" || len(r.PeerDeviceID) > MaxDeviceIDBytes || !validHash(r.AcceptanceHash) {
			return ErrInvalidRecord
		}
	case r.Role == RoleRecipient && r.State == StateAccepted:
		if strings.TrimSpace(r.PeerDeviceID) == "" || len(r.PeerDeviceID) > MaxDeviceIDBytes || !validHash(r.AcceptanceHash) {
			return ErrInvalidRecord
		}
	default:
		return ErrInvalidRecord
	}
	return nil
}

type Offer struct {
	EnrollmentID    string
	VaultID         string
	VaultCreatedAt  time.Time
	RetentionDays   int
	SourceDeviceID  string
	SourcePublicKey ed25519.PublicKey
	VaultKey        []byte
	IssuedAt        time.Time
	ExpiresAt       time.Time
}

func (o Offer) Validate() error {
	if strings.TrimSpace(o.EnrollmentID) == "" || len(o.EnrollmentID) > MaxEnrollmentIDBytes || strings.TrimSpace(o.VaultID) == "" || o.VaultCreatedAt.IsZero() || o.VaultCreatedAt.After(o.IssuedAt) || o.RetentionDays < 1 || o.RetentionDays > 3650 || strings.TrimSpace(o.SourceDeviceID) == "" || len(o.SourcePublicKey) != ed25519.PublicKeySize || len(o.VaultKey) != 32 || o.IssuedAt.IsZero() || o.ExpiresAt.Sub(o.IssuedAt) < MinValidity || o.ExpiresAt.Sub(o.IssuedAt) > MaxValidity {
		return ErrInvalidRecord
	}
	return nil
}

type Acceptance struct {
	EnrollmentID    string
	VaultID         string
	OfferHash       string
	SourceDeviceID  string
	TargetDeviceID  string
	TargetPublicKey ed25519.PublicKey
	AcceptedAt      time.Time
	ExpiresAt       time.Time
}

func (a Acceptance) Validate() error {
	if strings.TrimSpace(a.EnrollmentID) == "" || len(a.EnrollmentID) > MaxEnrollmentIDBytes || strings.TrimSpace(a.VaultID) == "" || !validHash(a.OfferHash) || strings.TrimSpace(a.SourceDeviceID) == "" || strings.TrimSpace(a.TargetDeviceID) == "" || len(a.TargetPublicKey) != ed25519.PublicKeySize || a.AcceptedAt.IsZero() || a.ExpiresAt.Before(a.AcceptedAt) {
		return ErrInvalidRecord
	}
	return nil
}

func (o Offer) ActiveAt(now time.Time) bool {
	return o.Validate() == nil && !now.UTC().Before(o.IssuedAt.Add(-MaxClockSkew)) && !now.UTC().After(o.ExpiresAt)
}

func (a Acceptance) ActiveAt(now time.Time) bool {
	return a.Validate() == nil && !now.UTC().Before(a.AcceptedAt.Add(-MaxClockSkew)) && !now.UTC().After(a.ExpiresAt)
}

func validHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == value
}
