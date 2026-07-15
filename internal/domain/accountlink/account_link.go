package accountlink

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

const MaxReferenceLength = 256

var (
	ErrInvalidRecord = errors.New("invalid account link record")
	referencePattern = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)
)

type State string

const (
	StateLinked   State = "linked"
	StateUnlinked State = "unlinked"
)

type VerifiedIdentity struct {
	SubjectRef        string
	WorkspaceRef      string
	ConsentReceiptRef string
	VerifiedAt        time.Time
}

func (v VerifiedIdentity) Validate() error {
	if !validReference(v.SubjectRef) || !validOptionalReference(v.WorkspaceRef) || !validReference(v.ConsentReceiptRef) || v.VerifiedAt.IsZero() {
		return fmt.Errorf("%w: verified identity references or timestamp are invalid", ErrInvalidRecord)
	}
	return nil
}

type Record struct {
	MembershipID      string
	VaultID           string
	State             State
	Revision          int
	SubjectRef        string
	WorkspaceRef      string
	ConsentReceiptRef string
	CreatedAt         time.Time
	LinkedAt          time.Time
	UpdatedAt         time.Time
	LastVerifiedAt    time.Time
	LastEventID       string
}

func (r Record) Validate() error {
	if r.MembershipID == "" || r.VaultID == "" || r.Revision < 1 || r.CreatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) || r.LastEventID == "" {
		return fmt.Errorf("%w: membership identity or lifecycle metadata is invalid", ErrInvalidRecord)
	}
	switch r.State {
	case StateLinked:
		identity := VerifiedIdentity{SubjectRef: r.SubjectRef, WorkspaceRef: r.WorkspaceRef, ConsentReceiptRef: r.ConsentReceiptRef, VerifiedAt: r.LastVerifiedAt}
		if identity.Validate() != nil || r.LinkedAt.IsZero() || r.LinkedAt.Before(r.CreatedAt) || r.UpdatedAt.Before(r.LinkedAt) || r.LastVerifiedAt.After(r.UpdatedAt) {
			return fmt.Errorf("%w: linked membership is missing verified references", ErrInvalidRecord)
		}
	case StateUnlinked:
		if r.SubjectRef != "" || r.WorkspaceRef != "" || r.ConsentReceiptRef != "" || !r.LinkedAt.IsZero() || !r.LastVerifiedAt.IsZero() {
			return fmt.Errorf("%w: unlinked membership retains active account references", ErrInvalidRecord)
		}
	default:
		return fmt.Errorf("%w: unknown account link state", ErrInvalidRecord)
	}
	return nil
}

func validReference(value string) bool {
	return value != "" && len(value) <= MaxReferenceLength && referencePattern.MatchString(value)
}

func validOptionalReference(value string) bool {
	return value == "" || validReference(value)
}
