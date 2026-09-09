package memorykernel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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

func (s *Service) ListAll(ctx context.Context, vaultID string, limit int) ([]memory.Record, error) {
	if ctx == nil || strings.TrimSpace(vaultID) == "" || limit < 1 || limit > 200 {
		return nil, ErrInvalidRequest
	}
	return s.store.ListMemories(ctx, memorystore.ListInput{VaultID: vaultID, Limit: limit})
}

func (s *Service) ExpireDue(ctx context.Context, vaultID string, at time.Time, limit int) ([]memory.Record, error) {
	if ctx == nil || strings.TrimSpace(vaultID) == "" || at.IsZero() || limit < 1 || limit > 200 {
		return nil, ErrInvalidRequest
	}
	at = at.UTC()
	records, err := s.store.ListMemories(ctx, memorystore.ListInput{VaultID: vaultID, Limit: limit, ExpiredAt: at})
	if err != nil {
		return nil, err
	}
	result := make([]memory.Record, 0)
	for _, record := range records {
		if (record.State != memory.StateApproved && record.State != memory.StateStable) || !record.IsExpired(at) {
			continue
		}
		updated, err := s.store.TransitionMemory(ctx, memorystore.TransitionInput{
			VaultID: vaultID, MemoryID: record.ID, ExpectedRevision: record.Revision, NextState: memory.StateStale,
			Reason: "memory validity period expired", OccurredAt: at,
			IdempotencyKey: fmt.Sprintf("memory-expire:%s:%d:%d", record.ID, record.Revision, record.ExpiresAt.UnixNano()),
		})
		if err != nil {
			return nil, err
		}
		result = append(result, updated)
	}
	return result, nil
}
