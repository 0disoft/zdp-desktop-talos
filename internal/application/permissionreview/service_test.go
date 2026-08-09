package permissionreview

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/execution"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/permission"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/executionstore"
)

func TestResolveOwnsGrantExpiryAndRejectsWorkspaceScope(t *testing.T) {
	store := &reviewStore{}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 13, 1, 2, 3, 0, time.UTC)
	service.now = func() time.Time { return now }

	if _, err := service.Resolve(context.Background(), "vault", "request", permission.OutcomeAllowWorkspace, "workspace"); !errors.Is(err, ErrInvalidResolution) {
		t.Fatalf("workspace outcome error=%v", err)
	}
	if store.resolveCalls != 0 {
		t.Fatal("workspace outcome reached the store")
	}

	if _, err := service.Resolve(context.Background(), "vault", "request", permission.OutcomeAllowOnce, "once"); err != nil {
		t.Fatal(err)
	}
	if store.last.Outcome != permission.OutcomeAllowOnce || !store.last.ExpiresAt.Equal(now.Add(AllowOnceTTL)) {
		t.Fatalf("once resolution=%+v", store.last)
	}
	if _, err := service.Resolve(context.Background(), "vault", "request", permission.OutcomeAllowTask, "task"); err != nil {
		t.Fatal(err)
	}
	if !store.last.ExpiresAt.Equal(now.Add(TaskGrantTTL)) {
		t.Fatalf("task expiry=%s", store.last.ExpiresAt)
	}
}

type reviewStore struct {
	last         executionstore.ResolvePermissionRequestInput
	resolveCalls int
}

func (*reviewStore) CreatePermissionRequest(context.Context, executionstore.CreatePermissionRequestInput) (permission.Request, error) {
	return permission.Request{}, errors.New("not used")
}
func (*reviewStore) ListOpenPermissionRequests(context.Context, string, string, int) ([]permission.Request, error) {
	return nil, nil
}
func (s *reviewStore) ResolvePermissionRequest(_ context.Context, input executionstore.ResolvePermissionRequestInput) (executionstore.PermissionResolution, error) {
	s.last, s.resolveCalls = input, s.resolveCalls+1
	return executionstore.PermissionResolution{}, nil
}
func (*reviewStore) SavePermissionGrant(context.Context, executionstore.SaveGrantInput) (permission.Grant, error) {
	return permission.Grant{}, errors.New("not used")
}
func (*reviewStore) ListActivePermissionGrants(context.Context, string, string) ([]permission.Grant, error) {
	return nil, nil
}
func (*reviewStore) PrepareAttempt(context.Context, executionstore.PrepareAttemptInput) (executionstore.Prepared, error) {
	return executionstore.Prepared{}, errors.New("not used")
}
func (*reviewStore) FinishAttempt(context.Context, executionstore.FinishAttemptInput) (executionstore.FinishedAttempt, error) {
	return executionstore.FinishedAttempt{}, errors.New("not used")
}
func (*reviewStore) FinishExecution(context.Context, executionstore.FinishExecutionInput) (executionstore.FinishedExecution, error) {
	return executionstore.FinishedExecution{}, errors.New("not used")
}
func (*reviewStore) FinishRun(context.Context, executionstore.FinishRunInput) (execution.Run, error) {
	return execution.Run{}, errors.New("not used")
}
func (*reviewStore) ReconcilePendingAttempts(context.Context, string, time.Time) (int, error) {
	return 0, nil
}
