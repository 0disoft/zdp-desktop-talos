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

type PatchDiff struct {
	Path          string `json:"path"`
	Binary        bool   `json:"binary"`
	Truncated     bool   `json:"truncated"`
	OmittedReason string `json:"omitted_reason,omitempty"`
	Text          string `json:"text,omitempty"`
	AddedLines    int    `json:"added_lines"`
	DeletedLines  int    `json:"deleted_lines"`
	Findings      int    `json:"findings"`
}

type PatchReviewStatus struct {
	TaskID           string         `json:"task_id"`
	ContractRevision int            `json:"contract_revision"`
	BaselineCommit   string         `json:"baseline_commit"`
	Status           string         `json:"status"`
	Reason           string         `json:"reason"`
	StateHash        string         `json:"state_hash"`
	PatchHash        string         `json:"patch_hash"`
	Changes          []PatchChange  `json:"changes"`
	Diffs            []PatchDiff    `json:"diffs"`
	SecretFindings   int            `json:"secret_findings"`
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
	vault     *VaultService
	workspace *WorkspaceService
	factory   PatchReviewFactory
	err       error
}

func NewPatchReviewService(vault *VaultService, workspace *WorkspaceService, factory PatchReviewFactory, initializationError error) *PatchReviewService {
	return &PatchReviewService{vault: vault, workspace: workspace, factory: factory, err: initializationError}
}

func (s *PatchReviewService) GetTaskReview(request PatchReviewRequest) PatchReviewResult {
	correlationID := normalizeCorrelationID(request.CorrelationID)
	if s == nil || s.vault == nil || s.workspace == nil || s.factory == nil || strings.TrimSpace(request.TaskID) == "" {
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
	snapshot, err := s.workspace.decisionSnapshot()
	if err != nil {
		mapped := MapError(err, correlationID)
		return PatchReviewResult{Error: &mapped}
	}
	lease, err := s.vault.acquireSessionLease(context.Background())
	if err != nil {
		mapped := MapError(patchreview.ErrInvalidRequest, correlationID)
		mapped.Code, mapped.Message = "VAULT_NOT_OPEN", "패치를 확인하려면 Vault를 먼저 열어 주세요."
		return PatchReviewResult{Error: &mapped}
	}
	defer lease.Release()
	store, err := lease.Session.ExecutionDatabase()
	if err != nil {
		mapped := MapError(err, correlationID)
		return PatchReviewResult{Error: &mapped}
	}
	service, err := s.factory.NewPatchReview(store)
	if err != nil {
		mapped := MapError(err, correlationID)
		return PatchReviewResult{Error: &mapped}
	}
	review, err := service.GetForWorkspace(lease.Context, strings.TrimSpace(request.TaskID), snapshot.Root, snapshot.BaselineCommit)
	if err != nil {
		mapped := MapError(err, correlationID)
		return PatchReviewResult{Error: &mapped}
	}
	status := PatchReviewStatus{TaskID: review.TaskID, ContractRevision: review.ContractRevision, BaselineCommit: review.BaselineCommit, Status: string(review.Status), Reason: review.Reason, StateHash: review.StateHash, PatchHash: review.PatchHash, Changes: make([]PatchChange, 0, len(review.Changes)), Diffs: make([]PatchDiff, 0, len(review.Diffs)), SecretFindings: review.SecretFindings}
	for _, change := range review.Changes {
		status.Changes = append(status.Changes, patchChangeDTO(change))
	}
	for _, diff := range review.Diffs {
		status.Diffs = append(status.Diffs, PatchDiff{Path: diff.Path, Binary: diff.Binary, Truncated: diff.Truncated, OmittedReason: diff.OmittedReason, Text: diff.Text, AddedLines: diff.AddedLines, DeletedLines: diff.DeletedLines, Findings: diff.Findings})
	}
	if review.Evidence != nil {
		status.Evidence = &PatchEvidence{ID: review.Evidence.ID, ContractRevision: review.Evidence.ContractRevision, CommandIndex: review.Evidence.CommandIndex, StateHash: review.Evidence.WorktreeStateHash, FinishedAt: review.Evidence.FinishedAt.UTC().Format(time.RFC3339Nano)}
	}
	return PatchReviewResult{Review: &status}
}

func patchChangeDTO(change workspace.Change) PatchChange {
	return PatchChange{Path: change.Path, OriginalPath: change.OriginalPath, Kind: string(change.Kind), IndexStatus: string([]byte{change.IndexStatus}), WorktreeStatus: string([]byte{change.WorktreeStatus})}
}
