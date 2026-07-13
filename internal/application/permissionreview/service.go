package permissionreview

import (
	"context"
	"errors"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/permission"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/executionstore"
)

const (
	AllowOnceTTL = 15 * time.Minute
	TaskGrantTTL = 24 * time.Hour
	DenyTTL      = 24 * time.Hour
)

var ErrInvalidResolution = errors.New("invalid permission review resolution")

type Service struct {
	store executionstore.Store
	now   func() time.Time
}

func New(store executionstore.Store) (*Service, error) {
	if store == nil {
		return nil, ErrInvalidResolution
	}
	return &Service{store: store, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *Service) List(ctx context.Context, vaultID, taskID string) ([]permission.Request, error) {
	if ctx == nil || vaultID == "" || taskID == "" {
		return nil, ErrInvalidResolution
	}
	return s.store.ListOpenPermissionRequests(ctx, vaultID, taskID, 256)
}

func (s *Service) Resolve(ctx context.Context, vaultID, requestID string, outcome permission.Outcome, idempotencyKey string) (executionstore.PermissionResolution, error) {
	if ctx == nil || vaultID == "" || requestID == "" || idempotencyKey == "" || len(idempotencyKey) > 128 {
		return executionstore.PermissionResolution{}, ErrInvalidResolution
	}
	var ttl time.Duration
	switch outcome {
	case permission.OutcomeAllowOnce:
		ttl = AllowOnceTTL
	case permission.OutcomeAllowTask:
		ttl = TaskGrantTTL
	case permission.OutcomeDeny:
		ttl = DenyTTL
	default:
		return executionstore.PermissionResolution{}, ErrInvalidResolution
	}
	now := s.now().UTC()
	return s.store.ResolvePermissionRequest(ctx, executionstore.ResolvePermissionRequestInput{VaultID: vaultID, RequestID: requestID, Outcome: outcome, OccurredAt: now, ExpiresAt: now.Add(ttl), IdempotencyKey: idempotencyKey})
}
