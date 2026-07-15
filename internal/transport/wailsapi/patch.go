package wailsapi

import (
	"context"
	"errors"
	"strings"

	"github.com/0disoft/zdp-desktop-talos/internal/application/patchcommand"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/patchaction"
)

type PatchCommandRequest struct {
	TaskID            string `json:"task_id"`
	ExpectedRevision  int    `json:"expected_revision"`
	ExpectedPatchHash string `json:"expected_patch_hash"`
	RequestID         string `json:"request_id"`
	CorrelationID     string `json:"correlation_id"`
}

type PatchCommandStatus struct {
	ActionID      string `json:"action_id"`
	Kind          string `json:"kind"`
	State         string `json:"state"`
	TaskStatus    string `json:"task_status"`
	SafeErrorCode string `json:"safe_error_code,omitempty"`
	Replayed      bool   `json:"replayed"`
}

type PatchCommandResult struct {
	Command *PatchCommandStatus `json:"command,omitempty"`
	Error   *TalosError         `json:"error,omitempty"`
}

type PatchCommandFactory interface {
	NewPatchCommand(patchcommand.Store) (*patchcommand.Service, error)
}

type PatchService struct {
	vault   *VaultService
	factory PatchCommandFactory
	err     error
}

func NewPatchService(vault *VaultService, factory PatchCommandFactory, initializationError error) *PatchService {
	return &PatchService{vault: vault, factory: factory, err: initializationError}
}

func (s *PatchService) ApplyPatch(request PatchCommandRequest) PatchCommandResult {
	return s.execute(request, patchaction.KindApply)
}

func (s *PatchService) DiscardPatch(request PatchCommandRequest) PatchCommandResult {
	return s.execute(request, patchaction.KindDiscard)
}

func (s *PatchService) execute(request PatchCommandRequest, kind patchaction.Kind) PatchCommandResult {
	correlationID := normalizeCorrelationID(request.CorrelationID)
	if s == nil || s.vault == nil || s.factory == nil || strings.TrimSpace(request.TaskID) == "" || strings.TrimSpace(request.RequestID) == "" {
		cause := patchcommand.ErrInvalidRequest
		if s == nil || s.vault == nil || s.factory == nil {
			cause = errPatchCommandUnavailable
			if s != nil && s.err != nil {
				cause = errors.Join(cause, s.err)
			}
		}
		mapped := MapError(cause, correlationID)
		return PatchCommandResult{Error: &mapped}
	}
	s.vault.mu.Lock()
	defer s.vault.mu.Unlock()
	if s.vault.session == nil {
		mapped := MapError(patchcommand.ErrInvalidRequest, correlationID)
		mapped.Code, mapped.Message = "VAULT_NOT_OPEN", "패치를 처리하려면 Vault를 먼저 열어 주세요."
		return PatchCommandResult{Error: &mapped}
	}
	store, err := s.vault.session.PatchDatabase()
	if err != nil {
		mapped := MapError(err, correlationID)
		return PatchCommandResult{Error: &mapped}
	}
	service, err := s.factory.NewPatchCommand(store)
	if err != nil {
		mapped := MapError(err, correlationID)
		return PatchCommandResult{Error: &mapped}
	}
	result, err := service.Execute(context.Background(), patchcommand.Request{TaskID: strings.TrimSpace(request.TaskID), Kind: kind, ExpectedRevision: request.ExpectedRevision, ExpectedPatchHash: strings.TrimSpace(request.ExpectedPatchHash), IdempotencyKey: strings.TrimSpace(request.RequestID)})
	if err != nil {
		mapped := MapError(err, correlationID)
		return PatchCommandResult{Error: &mapped}
	}
	return PatchCommandResult{Command: &PatchCommandStatus{ActionID: result.Action.ID, Kind: string(result.Action.Kind), State: string(result.Action.State), TaskStatus: string(result.TaskStatus), SafeErrorCode: result.Action.SafeErrorCode, Replayed: result.Replayed}}
}
