package workspaceremap

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspacemapping"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/repository"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/workspacestore"
)

var (
	ErrInvalidRequest   = errors.New("invalid workspace remap request")
	ErrBaselineNotFound = errors.New("required task baseline is not present in the selected repository")
)

type MapInput struct {
	VaultID          string
	TaskID           string
	LocalPath        string
	ExpectedRevision int
	OccurredAt       time.Time
	IdempotencyKey   string
}

type Service struct {
	store     workspacestore.Store
	inspector repository.Inspector
	verifier  repository.BaselineVerifier
}

func New(store workspacestore.Store, inspector repository.Inspector, verifier repository.BaselineVerifier) (*Service, error) {
	if store == nil || inspector == nil || verifier == nil {
		return nil, ErrInvalidRequest
	}
	return &Service{store: store, inspector: inspector, verifier: verifier}, nil
}

func (s *Service) Requirement(ctx context.Context, vaultID, taskID string) (workspacemapping.Requirement, error) {
	if ctx == nil || strings.TrimSpace(vaultID) == "" || strings.TrimSpace(taskID) == "" {
		return workspacemapping.Requirement{}, ErrInvalidRequest
	}
	return s.store.GetTaskWorkspace(ctx, vaultID, taskID)
}

func (s *Service) Map(ctx context.Context, input MapInput) (workspacemapping.Record, bool, error) {
	if ctx == nil || strings.TrimSpace(input.VaultID) == "" || strings.TrimSpace(input.TaskID) == "" || strings.TrimSpace(input.LocalPath) == "" || input.ExpectedRevision < 0 || strings.TrimSpace(input.IdempotencyKey) == "" {
		return workspacemapping.Record{}, false, ErrInvalidRequest
	}
	requirement, err := s.store.GetTaskWorkspace(ctx, input.VaultID, input.TaskID)
	if err != nil {
		return workspacemapping.Record{}, false, err
	}
	snapshot, err := s.inspector.Inspect(ctx, input.LocalPath)
	if err != nil {
		return workspacemapping.Record{}, false, err
	}
	contains, err := s.verifier.ContainsCommit(ctx, snapshot.Root, requirement.BaselineCommit)
	if err != nil {
		return workspacemapping.Record{}, false, err
	}
	if !contains {
		return workspacemapping.Record{}, false, ErrBaselineNotFound
	}
	return s.store.BindWorkspaceMapping(ctx, workspacestore.BindInput{
		WorkspaceID: requirement.WorkspaceID, VaultID: input.VaultID, SourceWorkspaceHash: requirement.SourceWorkspaceHash,
		LocalRoot: snapshot.Root, VerifiedBaseline: requirement.BaselineCommit, ExpectedRevision: input.ExpectedRevision,
		OccurredAt: input.OccurredAt, IdempotencyKey: input.IdempotencyKey,
	})
}

func (s *Service) Revoke(ctx context.Context, input workspacestore.RevokeInput) (workspacemapping.Record, bool, error) {
	if ctx == nil {
		return workspacemapping.Record{}, false, ErrInvalidRequest
	}
	return s.store.RevokeWorkspaceMapping(ctx, input)
}
