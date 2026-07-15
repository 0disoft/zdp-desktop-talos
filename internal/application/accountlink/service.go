package accountlink

import (
	"context"
	"errors"
	"strings"

	accountdomain "github.com/0disoft/zdp-desktop-talos/internal/domain/accountlink"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/accountidentity"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/accountstore"
)

var ErrInvalidRequest = errors.New("invalid account link request")

type LinkRequest struct {
	VaultID          string
	ChallengeID      string
	CorrelationID    string
	ExpectedRevision int
	IdempotencyKey   string
}

type UnlinkRequest struct {
	VaultID          string
	ExpectedRevision int
	IdempotencyKey   string
}

type Service struct {
	store    accountstore.Store
	verifier accountidentity.Verifier
}

func New(store accountstore.Store, verifier accountidentity.Verifier) (*Service, error) {
	if store == nil || verifier == nil {
		return nil, ErrInvalidRequest
	}
	return &Service{store: store, verifier: verifier}, nil
}

func (s *Service) Link(ctx context.Context, request LinkRequest) (accountdomain.Record, error) {
	request.VaultID = strings.TrimSpace(request.VaultID)
	request.ChallengeID = strings.TrimSpace(request.ChallengeID)
	request.CorrelationID = strings.TrimSpace(request.CorrelationID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if ctx == nil || request.VaultID == "" || !validIdentifier(request.ChallengeID) || !validIdentifier(request.CorrelationID) || request.ExpectedRevision < 0 || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 128 {
		return accountdomain.Record{}, ErrInvalidRequest
	}
	identity, err := s.verifier.Verify(ctx, accountidentity.Challenge{ID: request.ChallengeID, CorrelationID: request.CorrelationID})
	if err != nil {
		return accountdomain.Record{}, err
	}
	if err := identity.Validate(); err != nil {
		return accountdomain.Record{}, accountidentity.ErrRejected
	}
	return s.store.LinkAccount(ctx, accountstore.LinkInput{VaultID: request.VaultID, ExpectedRevision: request.ExpectedRevision, Identity: identity, IdempotencyKey: request.IdempotencyKey})
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.' || char == ':' {
			continue
		}
		return false
	}
	return true
}

func (s *Service) Unlink(ctx context.Context, request UnlinkRequest) (accountdomain.Record, error) {
	request.VaultID = strings.TrimSpace(request.VaultID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if ctx == nil || request.VaultID == "" || request.ExpectedRevision < 1 || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 128 {
		return accountdomain.Record{}, ErrInvalidRequest
	}
	return s.store.UnlinkAccount(ctx, accountstore.UnlinkInput{VaultID: request.VaultID, ExpectedRevision: request.ExpectedRevision, IdempotencyKey: request.IdempotencyKey})
}
