package wailsapi

import (
	"context"
	"errors"
	"strings"

	"github.com/0disoft/zdp-desktop-talos/internal/application/modelruntime"
	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
)

var (
	errModelUnavailable          = errors.New("model planning is unavailable")
	errModelConsentRequired      = errors.New("model egress consent is required")
	errModelConfigurationChanged = errors.New("model configuration changed")
)

type PlanningFactory interface {
	ProviderConfiguration() (providerKey, modelKey, credentialName, reasonCode string, ready bool)
	New(vaultbootstrap.PlanningDatabase) (modelruntime.Proposer, error)
}

type ModelProviderStatus struct {
	ProviderKey    string `json:"provider_key"`
	ModelKey       string `json:"model_key,omitempty"`
	CredentialName string `json:"credential_name"`
	Ready          bool   `json:"ready"`
	ReasonCode     string `json:"reason_code,omitempty"`
}

type PlanConsent struct {
	Confirmed        bool   `json:"confirmed"`
	ProviderKey      string `json:"provider_key"`
	ModelKey         string `json:"model_key"`
	WorkspaceRoot    string `json:"workspace_root"`
	BaselineCommit   string `json:"baseline_commit"`
	ContractRevision int    `json:"contract_revision"`
}

type PlanRequest struct {
	TaskID        string      `json:"task_id"`
	RequestID     string      `json:"request_id"`
	CorrelationID string      `json:"correlation_id"`
	Consent       PlanConsent `json:"consent"`
}

type PlanStepDTO struct {
	ID           string `json:"id"`
	Purpose      string `json:"purpose"`
	CommandIndex int    `json:"command_index"`
}

type PlanMemoryDTO struct {
	MemoryID  string `json:"memory_id"`
	Revision  int    `json:"revision"`
	Statement string `json:"statement"`
	Reason    string `json:"reason"`
}

type PlanReceiptDTO struct {
	ReceiptID         string `json:"receipt_id"`
	ProviderCallID    string `json:"provider_call_id"`
	ContextItems      int    `json:"context_items"`
	InputBytes        int    `json:"input_bytes"`
	OutputBytes       int    `json:"output_bytes"`
	RedactionCount    int    `json:"redaction_count"`
	InputTokens       int    `json:"input_tokens"`
	CachedInputTokens int    `json:"cached_input_tokens"`
	OutputTokens      int    `json:"output_tokens"`
}

type PlanProposalDTO struct {
	State            string          `json:"state"`
	TaskID           string          `json:"task_id"`
	ContractRevision int             `json:"contract_revision"`
	ProviderKey      string          `json:"provider_key"`
	ModelKey         string          `json:"model_key"`
	Summary          string          `json:"summary"`
	Steps            []PlanStepDTO   `json:"steps"`
	Memories         []PlanMemoryDTO `json:"memories"`
	Receipt          PlanReceiptDTO  `json:"receipt"`
}

type PlanResult struct {
	Proposal *PlanProposalDTO `json:"proposal,omitempty"`
	Error    *TalosError      `json:"error,omitempty"`
}

type PlanService struct {
	vault     *VaultService
	workspace *WorkspaceService
	factory   PlanningFactory
}

func NewPlanService(vault *VaultService, workspace *WorkspaceService, factory PlanningFactory) *PlanService {
	return &PlanService{vault: vault, workspace: workspace, factory: factory}
}

func (s *PlanService) ProviderStatus() ModelProviderStatus {
	if s == nil || s.factory == nil {
		return ModelProviderStatus{ProviderKey: "openai-responses", CredentialName: "OPENAI_API_KEY", ReasonCode: "MODEL_PROVIDER_UNAVAILABLE"}
	}
	provider, model, credential, reason, ready := s.factory.ProviderConfiguration()
	return ModelProviderStatus{ProviderKey: provider, ModelKey: model, CredentialName: credential, Ready: ready, ReasonCode: reason}
}

func (s *PlanService) Propose(request PlanRequest) PlanResult {
	correlationID := normalizeCorrelationID(request.CorrelationID)
	if s == nil || s.vault == nil || s.workspace == nil || s.factory == nil || strings.TrimSpace(request.TaskID) == "" || !validRequestID(request.RequestID) {
		return planError(modelruntime.ErrInvalidRequest, correlationID)
	}
	provider, model, _, reason, ready := s.factory.ProviderConfiguration()
	if !ready {
		mapped := MapError(errModelUnavailable, correlationID)
		if reason != "" {
			mapped.Details = map[string]string{"reason_code": reason}
		}
		return PlanResult{Error: &mapped}
	}
	consent := request.Consent
	if !consent.Confirmed {
		return planError(errModelConsentRequired, correlationID)
	}
	if consent.ProviderKey != provider || consent.ModelKey != model {
		return planError(errModelConfigurationChanged, correlationID)
	}
	snapshot, err := s.workspace.decisionSnapshot()
	if err != nil {
		return planError(err, correlationID)
	}
	if consent.WorkspaceRoot != snapshot.Root || consent.BaselineCommit != snapshot.BaselineCommit || consent.ContractRevision < 1 {
		return planError(errModelConfigurationChanged, correlationID)
	}

	s.vault.mu.Lock()
	defer s.vault.mu.Unlock()
	if s.vault.session == nil {
		return planError(vaultbootstrap.ErrNotOpen, correlationID)
	}
	database, err := s.vault.session.PlanningDatabase()
	if err != nil {
		return planError(err, correlationID)
	}
	record, err := database.GetTask(context.Background(), strings.TrimSpace(request.TaskID))
	if err != nil {
		return planError(err, correlationID)
	}
	if record.VaultID != s.vault.session.Record.ID || record.WorkspaceRoot != snapshot.Root || record.BaselineCommit != snapshot.BaselineCommit || record.CurrentRevision != consent.ContractRevision || record.Status != task.StatusContracted {
		return planError(vaultbootstrap.ErrTaskWorkspaceMismatch, correlationID)
	}
	runtime, err := s.factory.New(database)
	if err != nil {
		return planError(errors.Join(errModelUnavailable, err), correlationID)
	}
	result, err := runtime.Propose(context.Background(), modelruntime.Request{
		TaskID: record.ID, RequestID: strings.TrimSpace(request.RequestID),
		IdempotencyKey: "plan-proposal:" + strings.TrimSpace(request.RequestID),
	})
	if err != nil {
		return planError(err, correlationID)
	}
	if result.State != modelruntime.StateProposed || result.Receipt.TaskID != record.ID || result.Receipt.ContractRevision != record.CurrentRevision {
		return planError(modelruntime.ErrPlanRejected, correlationID)
	}
	steps := make([]PlanStepDTO, 0, len(result.Plan.Steps))
	for _, step := range result.Plan.Steps {
		steps = append(steps, PlanStepDTO{ID: step.ID, Purpose: step.Purpose, CommandIndex: step.Tool.CommandIndex})
	}
	memories := make([]PlanMemoryDTO, 0, len(result.MemoryContext.Items))
	for _, item := range result.MemoryContext.Items {
		memories = append(memories, PlanMemoryDTO{MemoryID: item.MemoryID, Revision: item.Revision, Statement: item.Content, Reason: item.Reason})
	}
	proposal := PlanProposalDTO{
		State: string(result.State), TaskID: record.ID, ContractRevision: record.CurrentRevision,
		ProviderKey: provider, ModelKey: model, Summary: result.Plan.Summary, Steps: steps, Memories: memories,
		Receipt: PlanReceiptDTO{
			ReceiptID: result.Receipt.ID, ProviderCallID: result.Receipt.ProviderCallID,
			ContextItems: result.Receipt.ContextItems, InputBytes: result.Receipt.InputBytes, OutputBytes: result.Receipt.OutputBytes,
			RedactionCount: result.Receipt.RedactionCount, InputTokens: result.Receipt.Usage.InputTokens,
			CachedInputTokens: result.Receipt.Usage.CachedInputTokens, OutputTokens: result.Receipt.Usage.OutputTokens,
		},
	}
	return PlanResult{Proposal: &proposal}
}

func validRequestID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 96
}

func planError(err error, correlationID string) PlanResult {
	mapped := MapError(err, correlationID)
	return PlanResult{Error: &mapped}
}
