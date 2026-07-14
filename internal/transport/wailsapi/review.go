package wailsapi

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/application/patchreview"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspace"
)

type PatchReviewRequest struct {
	TaskID        string `json:"task_id"`
	CorrelationID string `json:"correlation_id"`
}

type PatchChange struct {
	Path           string `json:"path"`
	OriginalPath   string `json:"original_path,omitempty"`
	Kind           string `json:"kind"`
	IndexStatus    string `json:"index_status"`
	WorktreeStatus string `json:"worktree_status"`
}

type PatchEvidence struct {
	ID               string `json:"id"`
	ContractRevision int    `json:"contract_revision"`
	CommandIndex     int    `json:"command_index"`
	StateHash        string `json:"state_hash"`
	FinishedAt       string `json:"finished_at"`
}

type PatchReviewStatus struct {
	TaskID           string         `json:"task_id"`
	ContractRevision int            `json:"contract_revision"`
	BaselineCommit   string         `json:"baseline_commit"`
	Status           string         `json:"status"`
	Reason           string         `json:"reason"`
	StateHash        string         `json:"state_hash"`
	Changes          []PatchChange  `json:"changes"`
	Evidence         *PatchEvidence `json:"evidence,omitempty"`
}

type PatchReviewResult struct {
	Review *PatchReviewStatus `json:"review,omitempty"`
	Error  *TalosError        `json:"error,omitempty"`
}

type PatchReviewFactory interface {
	NewPatchReview(patchreview.Store) (*patchreview.Service, error)
}

type PatchReviewService struct {
	vault   *VaultService
	factory PatchReviewFactory
	err     error
}

func NewPatchReviewService(vault *VaultService, factory PatchReviewFactory, initializationError error) *PatchReviewService {
	return &PatchReviewService{vault: vault, factory: factory, err: initializationError}
}

func (s *PatchReviewService) GetTaskReview(request PatchReviewRequest) PatchReviewResult {
	correlationID := normalizeCorrelationID(request.CorrelationID)
	if s == nil || s.vault == nil || s.factory == nil || strings.TrimSpace(request.TaskID) == "" {
		cause := patchreview.ErrInvalidRequest
		if s == nil || s.vault == nil || s.factory == nil {
			cause = errPatchReviewUnavailable
			if s != nil && s.err != nil {
				cause = errors.Join(cause, s.err)
			}
		}
		mapped := MapError(cause, correlationID)
		return PatchReviewResult{Error: &mapped}
	}
	s.vault.mu.Lock()
	defer s.vault.mu.Unlock()
	if s.vault.session == nil {
		mapped := MapError(patchreview.ErrInvalidRequest, correlationID)
		mapped.Code, mapped.Message = "VAULT_NOT_OPEN", "패치를 확인하려면 Vault를 먼저 열어 주세요."
		return PatchReviewResult{Error: &mapped}
	}
	store, err := s.vault.session.ExecutionDatabase()
	if err != nil {
		mapped := MapError(err, correlationID)
		return PatchReviewResult{Error: &mapped}
	}
	service, err := s.factory.NewPatchReview(store)
	if err != nil {
		mapped := MapError(err, correlationID)
		return PatchReviewResult{Error: &mapped}
	}
	review, err := service.Get(context.Background(), strings.TrimSpace(request.TaskID))
	if err != nil {
		mapped := MapError(err, correlationID)
		return PatchReviewResult{Error: &mapped}
	}
	status := PatchReviewStatus{TaskID: review.TaskID, ContractRevision: review.ContractRevision, BaselineCommit: review.BaselineCommit, Status: string(review.Status), Reason: review.Reason, StateHash: review.StateHash, Changes: make([]PatchChange, 0, len(review.Changes))}
	for _, change := range review.Changes {
		status.Changes = append(status.Changes, patchChangeDTO(change))
	}
	if review.Evidence != nil {
		status.Evidence = &PatchEvidence{ID: review.Evidence.ID, ContractRevision: review.Evidence.ContractRevision, CommandIndex: review.Evidence.CommandIndex, StateHash: review.Evidence.WorktreeStateHash, FinishedAt: review.Evidence.FinishedAt.UTC().Format(time.RFC3339Nano)}
	}
	return PatchReviewResult{Review: &status}
}

func patchChangeDTO(change workspace.Change) PatchChange {
	return PatchChange{Path: change.Path, OriginalPath: change.OriginalPath, Kind: string(change.Kind), IndexStatus: string([]byte{change.IndexStatus}), WorktreeStatus: string([]byte{change.WorktreeStatus})}
}
