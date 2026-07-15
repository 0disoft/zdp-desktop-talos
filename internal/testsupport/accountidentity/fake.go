package accountidentity

import (
	"context"
	"sync"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/accountlink"
	identityport "github.com/0disoft/zdp-desktop-talos/internal/ports/accountidentity"
)

type Verifier struct {
	mu         sync.Mutex
	challenges map[string]accountlink.VerifiedIdentity
	used       map[string]string
	err        error
}

func New(challenges map[string]accountlink.VerifiedIdentity) *Verifier {
	copied := make(map[string]accountlink.VerifiedIdentity, len(challenges))
	for key, value := range challenges {
		copied[key] = value
	}
	return &Verifier{challenges: copied, used: make(map[string]string)}
}

func Unavailable(err error) *Verifier {
	if err == nil {
		err = identityport.ErrUnavailable
	}
	return &Verifier{err: err, challenges: map[string]accountlink.VerifiedIdentity{}, used: make(map[string]string)}
}

func (v *Verifier) Verify(ctx context.Context, challenge identityport.Challenge) (accountlink.VerifiedIdentity, error) {
	if err := ctx.Err(); err != nil {
		return accountlink.VerifiedIdentity{}, err
	}
	if v == nil || challenge.ID == "" || challenge.CorrelationID == "" {
		return accountlink.VerifiedIdentity{}, identityport.ErrInvalidChallenge
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.err != nil {
		return accountlink.VerifiedIdentity{}, v.err
	}
	if correlationID, exists := v.used[challenge.ID]; exists {
		if correlationID != challenge.CorrelationID {
			return accountlink.VerifiedIdentity{}, identityport.ErrChallengeUsed
		}
		return v.challenges[challenge.ID], nil
	}
	identity, exists := v.challenges[challenge.ID]
	if !exists || identity.Validate() != nil {
		return accountlink.VerifiedIdentity{}, identityport.ErrRejected
	}
	v.used[challenge.ID] = challenge.CorrelationID
	return identity, nil
}
