package memorykernel

import (
	"context"
	"errors"
	"strings"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorystore"
)

var (
	ErrInvalidRequest = errors.New("invalid memory kernel request")
	ErrReviewRequired = errors.New("memory candidate requires an explicit review outcome")
)

type Service struct {
	store memorystore.Store
}

func New(store memorystore.Store) (*Service, error) {
	if store == nil {
		return nil, ErrInvalidRequest
	}
	return &Service{store: store}, nil
}

func (s *Service) CreateCandidate(ctx context.Context, input memorystore.CreateCandidateInput) (memory.Record, error) {
	if ctx == nil || strings.TrimSpace(input.SourceActor) == "" {
		return memory.Record{}, ErrInvalidRequest
	}
	return s.store.CreateMemoryCandidate(ctx, input)
}

func (s *Service) ReviewCandidate(ctx context.Context, input memorystore.TransitionInput) (memory.Record, error) {
	if ctx == nil {
		return memory.Record{}, ErrInvalidRequest
	}
	switch input.NextState {
	case memory.StateApproved, memory.StateRejected, memory.StateQuarantined:
		return s.store.TransitionMemory(ctx, input)
	default:
		return memory.Record{}, ErrReviewRequired
	}
}

func (s *Service) ChangeLifecycle(ctx context.Context, input memorystore.TransitionInput) (memory.Record, error) {
	if ctx == nil {
		return memory.Record{}, ErrInvalidRequest
	}
	switch input.NextState {
	case memory.StateApproved, memory.StateStable, memory.StateStale, memory.StateDeprecated, memory.StateSuperseded:
		return s.store.TransitionMemory(ctx, input)
	default:
		return memory.Record{}, ErrInvalidRequest
	}
}

func (s *Service) ListCandidates(ctx context.Context, vaultID string, limit int) ([]memory.Record, error) {
	if ctx == nil || strings.TrimSpace(vaultID) == "" {
		return nil, ErrInvalidRequest
	}
	return s.store.ListMemoryCandidates(ctx, vaultID, limit)
}
