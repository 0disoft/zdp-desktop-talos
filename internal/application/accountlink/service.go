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
	VaultID            string
	ProductRef         string
	ClientInstanceRef  string
	CorrelationID      string
	RequestedScopeRefs []string
	ExpectedRevision   int
	IdempotencyKey     string
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
	request.ProductRef = strings.TrimSpace(request.ProductRef)
	request.ClientInstanceRef = strings.TrimSpace(request.ClientInstanceRef)
	request.CorrelationID = strings.TrimSpace(request.CorrelationID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if ctx == nil || request.VaultID == "" || !validIdentifier(request.ProductRef) || !validIdentifier(request.ClientInstanceRef) || !validIdentifier(request.CorrelationID) || !validScopes(request.RequestedScopeRefs) || request.ExpectedRevision < 0 || !validIdentifier(request.IdempotencyKey) {
		return accountdomain.Record{}, ErrInvalidRequest
	}
	identity, err := s.verifier.Verify(ctx, accountidentity.Request{
		ProductRef: request.ProductRef, ClientInstanceRef: request.ClientInstanceRef,
		ClientCorrelationRef: request.CorrelationID, RequestedScopeRefs: append([]string(nil), request.RequestedScopeRefs...),
		IdempotencyKey: request.IdempotencyKey,
	})
	if err != nil {
		return accountdomain.Record{}, err
	}
	if err := identity.Validate(); err != nil {
		return accountdomain.Record{}, accountidentity.ErrRejected
	}
	return s.store.LinkAccount(ctx, accountstore.LinkInput{VaultID: request.VaultID, ExpectedRevision: request.ExpectedRevision, Identity: identity, IdempotencyKey: request.IdempotencyKey})
}

func validScopes(values []string) bool {
	if len(values) == 0 || len(values) > 16 {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validIdentifier(value) {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
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
