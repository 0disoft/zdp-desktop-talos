package accountidentity

import (
	"context"
	"errors"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/accountlink"
)

var (
	ErrInvalidRequest = errors.New("invalid account link verification request")
	ErrUnavailable    = errors.New("ZDP account verification is unavailable")
	ErrDenied         = errors.New("ZDP account verification was denied")
	ErrExpired        = errors.New("ZDP account link challenge expired")
	ErrConsumed       = errors.New("ZDP account link challenge was already consumed")
	ErrRejected       = errors.New("ZDP account verification response was rejected")
	ErrDisabled       = errors.New("ZDP account verification is disabled")
)

type Request struct {
	ProductRef           string
	ClientInstanceRef    string
	ClientCorrelationRef string
	RequestedScopeRefs   []string
	IdempotencyKey       string
}

type Verifier interface {
	Verify(context.Context, Request) (accountlink.VerifiedIdentity, error)
}
