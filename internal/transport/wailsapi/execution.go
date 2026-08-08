package wailsapi

import (
	"context"
	"errors"
	"strings"

	"github.com/0disoft/zdp-desktop-talos/internal/application/executionruntime"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/permission"
)

type ExecutionRequest struct {
	TaskID        string `json:"task_id"`
	CommandIndex  int    `json:"command_index"`
	RequestID     string `json:"request_id"`
	CorrelationID string `json:"correlation_id"`
}

type ExecutionStatus struct {
	State               string `json:"state"`
	Outcome             string `json:"outcome"`
	PermissionRequestID string `json:"permission_request_id,omitempty"`
	RunID               string `json:"run_id,omitempty"`
	AttemptID           string `json:"attempt_id,omitempty"`
	ExitCode            *int   `json:"exit_code,omitempty"`
	Replayed            bool   `json:"replayed"`
	EvidenceID          string `json:"evidence_id,omitempty"`
	ContractRevision    int    `json:"contract_revision,omitempty"`
	CommandIndex        int    `json:"command_index"`
	WorktreeStateHash   string `json:"worktree_state_hash,omitempty"`
}

type ExecutionResult struct {
	Execution *ExecutionStatus `json:"execution,omitempty"`
	Error     *TalosError      `json:"error,omitempty"`
}

type ExecutionService struct {
	vault               *VaultService
	workspace           *WorkspaceService
	factory             executionruntime.Factory
	initializationError error
}

func NewExecutionService(vault *VaultService, workspace *WorkspaceService, factory executionruntime.Factory, initializationError error) *ExecutionService {
	return &ExecutionService{vault: vault, workspace: workspace, factory: factory, initializationError: initializationError}
}

func (s *ExecutionService) ExecuteVerification(request ExecutionRequest) ExecutionResult {
	correlationID := normalizeCorrelationID(request.CorrelationID)
	if s == nil || s.vault == nil || s.workspace == nil || s.factory == nil {
		var err error
		if s != nil {
			err = s.initializationError
		}
		if err == nil {
			err = executionruntime.ErrInvalidRequest
		}
		mapped := MapError(errors.Join(errExecutionUnavailable, err), correlationID)
		return ExecutionResult{Error: &mapped}
	}
	requestID := strings.TrimSpace(request.RequestID)
	if strings.TrimSpace(request.TaskID) == "" || request.CommandIndex < 0 || requestID == "" || len(requestID) > 96 {
		mapped := MapError(executionruntime.ErrInvalidRequest, correlationID)
		return ExecutionResult{Error: &mapped}
	}
	snapshot, err := s.workspace.decisionSnapshot()
	if err != nil {
		mapped := MapError(err, correlationID)
		return ExecutionResult{Error: &mapped}
	}

	lease, err := s.vault.acquireSessionLease(context.Background())
	if err != nil {
		mapped := MapError(executionruntime.ErrInvalidRequest, correlationID)
		mapped.Code = "VAULT_NOT_OPEN"
		mapped.Message = "검증을 실행하려면 Vault를 먼저 열어 주세요."
		return ExecutionResult{Error: &mapped}
	}
	defer lease.Release()
	store, err := lease.Session.ExecutionDatabase()
	if err != nil {
		mapped := MapError(err, correlationID)
		return ExecutionResult{Error: &mapped}
	}
	coordinator, err := s.factory.New(store)
	if err != nil {
		mapped := MapError(errors.Join(errExecutionUnavailable, err), correlationID)
		return ExecutionResult{Error: &mapped}
	}
	result, err := coordinator.Execute(lease.Context, executionruntime.Request{
		TaskID: strings.TrimSpace(request.TaskID), CommandIndex: request.CommandIndex,
		IdempotencyKey: "task-verification:" + requestID, WorkspaceRoot: snapshot.Root, BaselineCommit: snapshot.BaselineCommit,
	})
	if err != nil {
		mapped := MapError(err, correlationID)
		return ExecutionResult{Error: &mapped}
	}
	status := ExecutionStatus{Outcome: string(result.Outcome), RunID: result.RunID, AttemptID: result.AttemptID, Replayed: result.Replayed}
	if result.Outcome == permission.OutcomeRequireReview {
		status.State = "review_required"
		status.PermissionRequestID = result.PermissionRequest.ID
	} else {
		if result.Evidence == nil {
			mapped := MapError(executionruntime.ErrJournalFailed, correlationID)
			return ExecutionResult{Error: &mapped}
		}
		status.State = "succeeded"
		exitCode := result.Tool.ExitCode
		status.ExitCode = &exitCode
		status.EvidenceID = result.Evidence.ID
		status.ContractRevision = result.Evidence.ContractRevision
		status.CommandIndex = result.Evidence.CommandIndex
		status.WorktreeStateHash = result.Evidence.WorktreeStateHash
	}
	return ExecutionResult{Execution: &status}
}
