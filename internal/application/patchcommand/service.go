package patchcommand

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/application/patchreview"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/patchaction"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/worktree"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/patchstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/repository"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
)

var (
	ErrInvalidRequest      = errors.New("invalid patch command request")
	ErrWorkspaceMismatch   = errors.New("patch task does not belong to the active workspace")
	ErrStaleReview         = errors.New("patch review is not fresh")
	ErrSecretFindings      = errors.New("patch review contains secret findings")
	ErrUnscannableChanges  = errors.New("patch review contains changes that were not fully scanned")
	ErrScopeViolation      = errors.New("patch escapes the task contract")
	ErrNoChanges           = errors.New("patch contains no changes")
	ErrActionUnresolved    = errors.New("patch action outcome requires review")
	ErrPatchConflict       = errors.New("primary worktree no longer matches the task baseline")
	ErrPatchApplyFailed    = errors.New("patch could not be applied")
	ErrPatchOutcomeUnknown = errors.New("patch mutation outcome is unknown")
)

type Store interface {
	taskstore.Store
	patchstore.Store
	patchreview.Store
}

type Reviewer interface {
	Get(context.Context, string) (patchreview.Result, error)
}

type Worktrees interface {
	Open(context.Context, repository.CreateWorktreeInput) (worktree.Record, error)
	Apply(context.Context, worktree.Record, string) error
	Discard(context.Context, worktree.Record, string) error
}

type Request struct {
	TaskID            string
	Kind              patchaction.Kind
	ExpectedRevision  int
	ExpectedPatchHash string
	IdempotencyKey    string
	WorkspaceRoot     string
	BaselineCommit    string
}

type Result struct {
	Action     patchaction.Record
	TaskStatus task.Status
	Replayed   bool
}

type Service struct {
	store     Store
	reviewer  Reviewer
	worktrees Worktrees
	now       func() time.Time
}

func New(store Store, reviewer Reviewer, worktrees Worktrees) (*Service, error) {
	if store == nil || reviewer == nil || worktrees == nil {
		return nil, ErrInvalidRequest
	}
	return &Service{store: store, reviewer: reviewer, worktrees: worktrees, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *Service) Execute(ctx context.Context, request Request) (Result, error) {
	request.TaskID = strings.TrimSpace(request.TaskID)
	request.ExpectedPatchHash = strings.TrimSpace(request.ExpectedPatchHash)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if ctx == nil || request.TaskID == "" || request.ExpectedRevision < 1 || len(request.ExpectedPatchHash) != 64 || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 120 || request.WorkspaceRoot == "" || request.BaselineCommit == "" || (request.Kind != patchaction.KindApply && request.Kind != patchaction.KindDiscard) {
		return Result{}, ErrInvalidRequest
	}
	record, err := s.store.GetTask(ctx, request.TaskID)
	if err != nil {
		return Result{}, err
	}
	if !sameWorkspace(record.WorkspaceRoot, request.WorkspaceRoot) || record.BaselineCommit != request.BaselineCommit {
		return Result{}, ErrWorkspaceMismatch
	}
	replayInput := patchstore.ReplayInput{VaultID: record.VaultID, TaskID: record.ID, Kind: request.Kind, ContractRevision: request.ExpectedRevision, PatchHash: request.ExpectedPatchHash, IdempotencyKey: request.IdempotencyKey}
	if previous, err := s.store.FindPatchAction(ctx, replayInput); err == nil {
		return replayAction(previous, record.Status)
	} else if !errors.Is(err, patchstore.ErrNotFound) {
		return Result{}, err
	}
	contract, err := s.store.GetTaskContract(ctx, record.ID, record.CurrentRevision)
	if err != nil {
		return Result{}, err
	}
	review, err := s.reviewer.Get(ctx, record.ID)
	if err != nil {
		return Result{}, err
	}
	if err := Decide(record, contract, review, request); err != nil {
		return Result{}, err
	}
	owned, err := s.worktrees.Open(ctx, repository.CreateWorktreeInput{TaskID: record.ID, RepositoryRoot: record.WorkspaceRoot, BaselineCommit: record.BaselineCommit, CreatedAt: s.now()})
	if err != nil {
		return Result{}, err
	}
	evidenceID := ""
	if request.Kind == patchaction.KindApply {
		evidenceID = review.Evidence.ID
	}
	prepared, err := s.store.PreparePatchAction(ctx, patchstore.PrepareInput{VaultID: record.VaultID, TaskID: record.ID, Kind: request.Kind, ContractRevision: request.ExpectedRevision, PatchHash: request.ExpectedPatchHash, WorktreeStateHash: review.StateHash, EvidenceID: evidenceID, OccurredAt: s.now(), IdempotencyKey: request.IdempotencyKey})
	if errors.Is(err, patchstore.ErrIdempotencyConflict) {
		// Another copy of this request may have prepared after our first lookup.
		// Compare caller identity instead of freshly generated evidence/time.
		if previous, lookupErr := s.store.FindPatchAction(ctx, replayInput); lookupErr == nil {
			return replayAction(previous, record.Status)
		}
	}
	if err != nil {
		return Result{}, err
	}
	result := Result{Action: prepared.Action, TaskStatus: record.Status, Replayed: prepared.Replayed}
	if prepared.Replayed {
		return replayAction(prepared.Action, record.Status)
	}

	mutationErr := s.mutate(ctx, request.Kind, owned, request.ExpectedPatchHash)
	nextState, code, publicErr := patchResult(mutationErr)
	finishCtx := context.WithoutCancel(ctx)
	finished, finishErr := s.store.FinishPatchAction(finishCtx, patchstore.FinishInput{VaultID: record.VaultID, ActionID: prepared.Action.ID, ExpectedState: patchaction.StatePending, NextState: nextState, SafeErrorCode: code, OccurredAt: s.now(), IdempotencyKey: request.IdempotencyKey + ":finish"})
	if finishErr != nil {
		return result, errors.Join(ErrPatchOutcomeUnknown, fmt.Errorf("persist patch outcome: %w", finishErr))
	}
	result.Action = finished
	if nextState == patchaction.StateSucceeded {
		result.TaskStatus = outcomeStatus(request.Kind)
	}
	return result, publicErr
}

func replayAction(action patchaction.Record, currentStatus task.Status) (Result, error) {
	result := Result{Action: action, TaskStatus: currentStatus, Replayed: true}
	switch action.State {
	case patchaction.StateSucceeded:
		result.TaskStatus = outcomeStatus(action.Kind)
		return result, nil
	case patchaction.StateFailed:
		return result, errorForCode(action.SafeErrorCode)
	default:
		return result, ErrActionUnresolved
	}
}

func (s *Service) mutate(ctx context.Context, kind patchaction.Kind, owned worktree.Record, patchHash string) error {
	if kind == patchaction.KindApply {
		return s.worktrees.Apply(ctx, owned, patchHash)
	}
	return s.worktrees.Discard(ctx, owned, patchHash)
}

func patchResult(err error) (patchaction.State, string, error) {
	switch {
	case err == nil:
		return patchaction.StateSucceeded, "", nil
	case errors.Is(err, repository.ErrPatchConflict):
		return patchaction.StateFailed, "PATCH_CONFLICT", ErrPatchConflict
	case errors.Is(err, repository.ErrPatchApplyFailed):
		return patchaction.StateFailed, "PATCH_APPLY_FAILED", ErrPatchApplyFailed
	default:
		return patchaction.StateUnknown, "PATCH_OUTCOME_UNKNOWN", ErrPatchOutcomeUnknown
	}
}

func errorForCode(code string) error {
	switch code {
	case "PATCH_CONFLICT":
		return ErrPatchConflict
	case "PATCH_APPLY_FAILED":
		return ErrPatchApplyFailed
	default:
		return ErrPatchOutcomeUnknown
	}
}

func outcomeStatus(kind patchaction.Kind) task.Status {
	if kind == patchaction.KindDiscard {
		return task.StatusDiscarded
	}
	return task.StatusCompleted
}

func sameWorkspace(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
