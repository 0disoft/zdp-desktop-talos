package accountidentity

import (
	"context"
	"errors"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/accountlink"
)

var (
	ErrInvalidChallenge = errors.New("invalid account link challenge")
	ErrUnavailable      = errors.New("ZDP account verification is unavailable")
	ErrRejected         = errors.New("ZDP account verification was rejected")
	ErrChallengeUsed    = errors.New("ZDP account link challenge was already consumed")
)

type Challenge struct {
	ID            string
	CorrelationID string
}

type Verifier interface {
	Verify(context.Context, Challenge) (accountlink.VerifiedIdentity, error)
}
