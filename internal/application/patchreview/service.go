package patchreview

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/verification"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspace"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/worktree"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/executionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/repository"
)

var ErrInvalidRequest = errors.New("invalid patch review request")

type Status string

const (
	StatusFresh      Status = "fresh"
	StatusStale      Status = "stale"
	StatusUnverified Status = "unverified"
)

type Store interface {
	GetTask(context.Context, string) (task.Record, error)
	GetLatestVerificationEvidence(context.Context, string, string) (verification.Evidence, error)
}

type Worktrees interface {
	Open(context.Context, repository.CreateWorktreeInput) (worktree.Record, error)
	Review(context.Context, worktree.Record) (repository.WorktreeReview, error)
}

type Result struct {
	TaskID           string
	ContractRevision int
	BaselineCommit   string
	Status           Status
	Reason           string
	StateHash        string
	Changes          []workspace.Change
	Evidence         *verification.Evidence
}

type Service struct {
	store     Store
	worktrees Worktrees
}

func New(store Store, worktrees Worktrees) (*Service, error) {
	if store == nil || worktrees == nil {
		return nil, ErrInvalidRequest
	}
	return &Service{store: store, worktrees: worktrees}, nil
}

func (s *Service) Get(ctx context.Context, taskID string) (Result, error) {
	if ctx == nil || strings.TrimSpace(taskID) == "" {
		return Result{}, ErrInvalidRequest
	}
	record, err := s.store.GetTask(ctx, strings.TrimSpace(taskID))
	if err != nil {
		return Result{}, err
	}
	owned, err := s.worktrees.Open(ctx, repository.CreateWorktreeInput{TaskID: record.ID, RepositoryRoot: record.WorkspaceRoot, BaselineCommit: record.BaselineCommit, CreatedAt: time.Now().UTC()})
	if err != nil {
		return Result{}, err
	}
	review, err := s.worktrees.Review(ctx, owned)
	if err != nil {
		return Result{}, err
	}
	result := Result{TaskID: record.ID, ContractRevision: record.CurrentRevision, BaselineCommit: record.BaselineCommit, Status: StatusUnverified, Reason: "no_evidence", StateHash: review.StateHash, Changes: review.Changes}
	evidence, err := s.store.GetLatestVerificationEvidence(ctx, record.VaultID, record.ID)
	if errors.Is(err, executionstore.ErrNotFound) {
		return result, nil
	}
	if err != nil {
		return Result{}, err
	}
	result.Evidence = &evidence
	switch {
	case evidence.BaselineCommit != record.BaselineCommit:
		result.Status, result.Reason = StatusStale, "baseline_changed"
	case evidence.ContractRevision != record.CurrentRevision:
		result.Status, result.Reason = StatusStale, "contract_changed"
	case evidence.WorktreeStateHash != review.StateHash:
		result.Status, result.Reason = StatusStale, "patch_changed"
	default:
		result.Status, result.Reason = StatusFresh, "fresh"
	}
	return result, nil
}
