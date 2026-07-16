package accountidentity

import (
	"context"
	"sync"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/accountlink"
	identityport "github.com/0disoft/zdp-desktop-talos/internal/ports/accountidentity"
)

type Verifier struct {
	mu      sync.Mutex
	results map[string]accountlink.VerifiedIdentity
	used    map[string]string
	err     error
}

func New(results map[string]accountlink.VerifiedIdentity) *Verifier {
	copied := make(map[string]accountlink.VerifiedIdentity, len(results))
	for key, value := range results {
		copied[key] = value
	}
	return &Verifier{results: copied, used: make(map[string]string)}
}

func Unavailable(err error) *Verifier {
	if err == nil {
		err = identityport.ErrUnavailable
	}
	return &Verifier{err: err, results: map[string]accountlink.VerifiedIdentity{}, used: make(map[string]string)}
}

func (v *Verifier) Verify(ctx context.Context, request identityport.Request) (accountlink.VerifiedIdentity, error) {
	if err := ctx.Err(); err != nil {
		return accountlink.VerifiedIdentity{}, err
	}
	if v == nil || request.IdempotencyKey == "" || request.ClientCorrelationRef == "" {
		return accountlink.VerifiedIdentity{}, identityport.ErrInvalidRequest
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.err != nil {
		return accountlink.VerifiedIdentity{}, v.err
	}
	if correlationID, exists := v.used[request.IdempotencyKey]; exists {
		if correlationID != request.ClientCorrelationRef {
			return accountlink.VerifiedIdentity{}, identityport.ErrConsumed
		}
		return v.results[request.IdempotencyKey], nil
	}
	identity, exists := v.results[request.IdempotencyKey]
	if !exists || identity.Validate() != nil {
		return accountlink.VerifiedIdentity{}, identityport.ErrRejected
	}
	v.used[request.IdempotencyKey] = request.ClientCorrelationRef
	return identity, nil
}
