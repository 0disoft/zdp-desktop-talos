package memorykernel

import (
	"context"
	"errors"
	"testing"

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

type fakeStore struct {
	transitionCalls int
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
