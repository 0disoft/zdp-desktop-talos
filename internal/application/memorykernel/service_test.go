package memorykernel

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorystore"
)

func TestReviewCandidateDoesNotPermitAutomaticStablePromotion(t *testing.T) {
	store := &fakeStore{}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ReviewCandidate(context.Background(), memorystore.TransitionInput{NextState: memory.StateStable})
	if !errors.Is(err, ErrReviewRequired) || store.transitionCalls != 0 {
		t.Fatalf("error=%v calls=%d", err, store.transitionCalls)
	}
	_, err = service.ReviewCandidate(context.Background(), memorystore.TransitionInput{NextState: memory.StateApproved})
	if err != nil || store.transitionCalls != 1 {
		t.Fatalf("error=%v calls=%d", err, store.transitionCalls)
	}
}

func TestExpireDueTransitionsOnlyExpiredActiveMemory(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 18, 2, 0, 0, 0, time.UTC)
	store := &expiryStore{records: []memory.Record{
		{ID: "expired", VaultID: "vault", State: memory.StateApproved, Revision: 2, ExpiresAt: now.Add(-time.Minute)},
		{ID: "future", VaultID: "vault", State: memory.StateStable, Revision: 3, ExpiresAt: now.Add(time.Minute)},
		{ID: "rejected", VaultID: "vault", State: memory.StateRejected, Revision: 2, ExpiresAt: now.Add(-time.Minute)},
	}}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.ExpireDue(context.Background(), "vault", now, 10)
	if err != nil || len(updated) != 1 || updated[0].ID != "expired" || updated[0].State != memory.StateStale || len(store.transitions) != 1 || store.transitions[0].OccurredAt != now {
		t.Fatalf("updated=%+v transitions=%+v error=%v", updated, store.transitions, err)
	}
}

type fakeStore struct {
	transitionCalls int
}

type expiryStore struct {
	records     []memory.Record
	transitions []memorystore.TransitionInput
}

func (*expiryStore) CreateMemoryCandidate(context.Context, memorystore.CreateCandidateInput) (memory.Record, error) {
	return memory.Record{}, nil
}
func (s *expiryStore) TransitionMemory(_ context.Context, input memorystore.TransitionInput) (memory.Record, error) {
	s.transitions = append(s.transitions, input)
	for index := range s.records {
		if s.records[index].ID == input.MemoryID {
			s.records[index].State = input.NextState
			s.records[index].Revision++
			return s.records[index], nil
		}
	}
	return memory.Record{}, memorystore.ErrNotFound
}
func (*expiryStore) GetMemory(context.Context, string, string) (memory.Record, error) {
	return memory.Record{}, memorystore.ErrNotFound
}
func (*expiryStore) ListMemoryCandidates(context.Context, string, int) ([]memory.Record, error) {
	return nil, nil
}
func (s *expiryStore) ListActiveMemories(context.Context, memorystore.ListActiveInput) ([]memory.Record, error) {
	return append([]memory.Record(nil), s.records...), nil
}
func (s *expiryStore) ListMemories(context.Context, memorystore.ListInput) ([]memory.Record, error) {
	return append([]memory.Record(nil), s.records...), nil
}

func (*fakeStore) CreateMemoryCandidate(context.Context, memorystore.CreateCandidateInput) (memory.Record, error) {
	return memory.Record{}, nil
}
func (s *fakeStore) TransitionMemory(context.Context, memorystore.TransitionInput) (memory.Record, error) {
	s.transitionCalls++
	return memory.Record{}, nil
}
func (*fakeStore) GetMemory(context.Context, string, string) (memory.Record, error) {
	return memory.Record{}, memorystore.ErrNotFound
}
func (*fakeStore) ListMemoryCandidates(context.Context, string, int) ([]memory.Record, error) {
	return nil, nil
}
func (*fakeStore) ListActiveMemories(context.Context, memorystore.ListActiveInput) ([]memory.Record, error) {
	return nil, nil
}
func (*fakeStore) ListMemories(context.Context, memorystore.ListInput) ([]memory.Record, error) {
	return nil, nil
}
