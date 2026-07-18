package wailsapi

import (
	"context"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/application/contextassembly"
	"github.com/0disoft/zdp-desktop-talos/internal/application/memorycompile"
	"github.com/0disoft/zdp-desktop-talos/internal/application/memorykernel"
	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorycontext"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorystore"
	"github.com/0disoft/zdp-desktop-talos/internal/security/redaction"
)

const (
	maxMemoryCandidates = 200
	maxContextItems     = 32
	maxContextBytes     = 64 << 10
)

type MemoryDTO struct {
	MemoryID         string   `json:"memory_id"`
	Kind             string   `json:"kind"`
	State            string   `json:"state"`
	Scope            string   `json:"scope"`
	Statement        string   `json:"statement"`
	Rationale        string   `json:"rationale"`
	GoalTerms        []string `json:"goal_terms"`
	EvidenceEventIDs []string `json:"evidence_event_ids"`
	SourceActor      string   `json:"source_actor"`
	Confidence       int      `json:"confidence"`
	Sensitivity      string   `json:"sensitivity"`
	Revision         int      `json:"revision"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
}

type MemoryContextDTO struct {
	MemoryID    string `json:"memory_id"`
	Revision    int    `json:"revision"`
	Statement   string `json:"statement"`
	Reason      string `json:"reason"`
	SourceRef   string `json:"source_ref"`
	Sensitivity string `json:"sensitivity"`
}

type MemoryListResult struct {
	Memories []MemoryDTO `json:"memories"`
	Error    *TalosError `json:"error,omitempty"`
}

type MemoryCompileRequest struct {
	TaskID        string `json:"task_id"`
	RequestID     string `json:"request_id"`
	CorrelationID string `json:"correlation_id"`
}

type MemoryCompileResult struct {
	Eligible int         `json:"eligible"`
	Memories []MemoryDTO `json:"memories"`
	Error    *TalosError `json:"error,omitempty"`
}

type MemoryReviewRequest struct {
	MemoryID         string `json:"memory_id"`
	ExpectedRevision int    `json:"expected_revision"`
	Outcome          string `json:"outcome"`
	Reason           string `json:"reason"`
	RequestID        string `json:"request_id"`
	CorrelationID    string `json:"correlation_id"`
}

type MemoryResult struct {
	Memory *MemoryDTO  `json:"memory,omitempty"`
	Error  *TalosError `json:"error,omitempty"`
}

type MemoryContextResult struct {
	Considered    int                `json:"considered"`
	SelectedBytes int                `json:"selected_bytes"`
	Items         []MemoryContextDTO `json:"items"`
	Error         *TalosError        `json:"error,omitempty"`
}

type MemoryService struct {
	vault     *VaultService
	workspace *WorkspaceService
}

func NewMemoryService(vault *VaultService, workspace *WorkspaceService) *MemoryService {
	return &MemoryService{vault: vault, workspace: workspace}
}

func (s *MemoryService) CompileTask(request MemoryCompileRequest) MemoryCompileResult {
	correlationID := normalizeCorrelationID(request.CorrelationID)
	if s.vault == nil || s.workspace == nil || strings.TrimSpace(request.TaskID) == "" || strings.TrimSpace(request.RequestID) == "" {
		return memoryCompileError(memorycompile.ErrInvalidRequest, correlationID)
	}
	snapshot, err := s.workspace.decisionSnapshot()
	if err != nil {
		return memoryCompileError(err, correlationID)
	}
	result, err := s.vault.compileTaskMemory(strings.TrimSpace(request.TaskID), snapshot.Root, snapshot.BaselineCommit)
	if err != nil {
		return memoryCompileError(err, correlationID)
	}
	return MemoryCompileResult{Eligible: result.Eligible, Memories: memoryDTOs(result.Memories)}
}

func (s *MemoryService) ListCandidates(correlationID string) MemoryListResult {
	correlationID = normalizeCorrelationID(correlationID)
	if s.vault == nil {
		return memoryListError(memorykernel.ErrInvalidRequest, correlationID)
	}
	records, err := s.vault.listMemoryCandidates(maxMemoryCandidates)
	if err != nil {
		return memoryListError(err, correlationID)
	}
	return MemoryListResult{Memories: memoryDTOs(records)}
}

func (s *MemoryService) Review(request MemoryReviewRequest) MemoryResult {
	correlationID := normalizeCorrelationID(request.CorrelationID)
	if s.vault == nil || strings.TrimSpace(request.MemoryID) == "" || request.ExpectedRevision < 1 || strings.TrimSpace(request.Reason) == "" || strings.TrimSpace(request.RequestID) == "" {
		return memoryError(memorystore.ErrInvalidCommand, correlationID)
	}
	nextState := memory.State(request.Outcome)
	if nextState != memory.StateApproved && nextState != memory.StateRejected && nextState != memory.StateQuarantined {
		return memoryError(memorykernel.ErrReviewRequired, correlationID)
	}
	record, err := s.vault.reviewMemory(memorystore.TransitionInput{
		MemoryID: strings.TrimSpace(request.MemoryID), ExpectedRevision: request.ExpectedRevision,
		NextState: nextState, Reason: strings.TrimSpace(request.Reason),
		IdempotencyKey: "memory-review:" + strings.TrimSpace(request.RequestID),
	})
	if err != nil {
		return memoryError(err, correlationID)
	}
	dto := memoryDTO(record)
	return MemoryResult{Memory: &dto}
}

func (s *MemoryService) ExplainCurrentTask(taskID, correlationID string) MemoryContextResult {
	correlationID = normalizeCorrelationID(correlationID)
	if s.vault == nil || s.workspace == nil || strings.TrimSpace(taskID) == "" {
		return memoryContextError(contextassembly.ErrInvalidRequest, correlationID)
	}
	snapshot, err := s.workspace.decisionSnapshot()
	if err != nil {
		return memoryContextError(err, correlationID)
	}
	result, err := s.vault.explainTaskMemory(strings.TrimSpace(taskID), snapshot.Root, snapshot.BaselineCommit)
	if err != nil {
		return memoryContextError(err, correlationID)
	}
	items := make([]MemoryContextDTO, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, MemoryContextDTO{MemoryID: item.MemoryID, Revision: item.Revision, Statement: item.Content, Reason: item.Reason, SourceRef: item.SourceRef, Sensitivity: string(item.Sensitivity)})
	}
	return MemoryContextResult{Considered: result.Considered, SelectedBytes: result.SelectedBytes, Items: items}
}

func (s *VaultService) compileTaskMemory(taskID, workspaceRoot, baselineCommit string) (memorycompile.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	database, vaultID, err := s.memoryDatabaseLocked()
	if err != nil {
		return memorycompile.Result{}, err
	}
	taskRecord, err := database.GetTask(context.Background(), taskID)
	if err != nil {
		return memorycompile.Result{}, err
	}
	if taskRecord.VaultID != vaultID || taskRecord.WorkspaceRoot != workspaceRoot || taskRecord.BaselineCommit != baselineCommit {
		return memorycompile.Result{}, vaultbootstrap.ErrTaskWorkspaceMismatch
	}
	compiler, err := memorycompile.New(database, database, database, redaction.NewScanner())
	if err != nil {
		return memorycompile.Result{}, err
	}
	return compiler.CompileTask(context.Background(), vaultID, taskID)
}

func (s *VaultService) listMemoryCandidates(limit int) ([]memory.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	database, vaultID, err := s.memoryDatabaseLocked()
	if err != nil {
		return nil, err
	}
	kernel, err := memorykernel.New(database)
	if err != nil {
		return nil, err
	}
	return kernel.ListCandidates(context.Background(), vaultID, limit)
}

func (s *VaultService) reviewMemory(input memorystore.TransitionInput) (memory.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	database, vaultID, err := s.memoryDatabaseLocked()
	if err != nil {
		return memory.Record{}, err
	}
	input.VaultID = vaultID
	kernel, err := memorykernel.New(database)
	if err != nil {
		return memory.Record{}, err
	}
	return kernel.ReviewCandidate(context.Background(), input)
}

func (s *VaultService) explainTaskMemory(taskID, workspaceRoot, baselineCommit string) (memorycontext.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	database, vaultID, err := s.memoryDatabaseLocked()
	if err != nil {
		return memorycontext.Result{}, err
	}
	taskRecord, err := database.GetTask(context.Background(), taskID)
	if err != nil {
		return memorycontext.Result{}, err
	}
	if taskRecord.VaultID != vaultID || taskRecord.WorkspaceRoot != workspaceRoot || taskRecord.BaselineCommit != baselineCommit {
		return memorycontext.Result{}, vaultbootstrap.ErrTaskWorkspaceMismatch
	}
	contract, err := database.GetTaskContract(context.Background(), taskID, taskRecord.CurrentRevision)
	if err != nil {
		return memorycontext.Result{}, err
	}
	assembler, err := contextassembly.New(database)
	if err != nil {
		return memorycontext.Result{}, err
	}
	return assembler.Assemble(context.Background(), memorycontext.Request{VaultID: vaultID, WorkspaceRoot: workspaceRoot, Goal: contract.Goal, AllowedPaths: contract.AllowedPaths, MaxCandidates: maxMemoryCandidates, MaxItems: maxContextItems, MaxBytes: maxContextBytes})
}

func (s *VaultService) memoryDatabaseLocked() (vaultbootstrap.MemoryDatabase, string, error) {
	if s.session == nil {
		return nil, "", vaultbootstrap.ErrNotOpen
	}
	database, err := s.session.MemoryDatabase()
	if err != nil {
		return nil, "", err
	}
	return database, s.session.Record.ID, nil
}

func memoryDTOs(records []memory.Record) []MemoryDTO {
	dtos := make([]MemoryDTO, 0, len(records))
	for _, record := range records {
		dtos = append(dtos, memoryDTO(record))
	}
	return dtos
}

func memoryDTO(record memory.Record) MemoryDTO {
	return MemoryDTO{MemoryID: record.ID, Kind: string(record.Kind), State: string(record.State), Scope: string(record.Scope.Kind), Statement: record.Statement, Rationale: record.Rationale, GoalTerms: append([]string(nil), record.Applicability.GoalTerms...), EvidenceEventIDs: append([]string(nil), record.EvidenceEventIDs...), SourceActor: record.SourceActor, Confidence: record.Confidence, Sensitivity: string(record.Sensitivity), Revision: record.Revision, CreatedAt: record.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: record.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}

func memoryCompileError(err error, correlationID string) MemoryCompileResult {
	mapped := MapError(err, correlationID)
	return MemoryCompileResult{Error: &mapped}
}

func memoryListError(err error, correlationID string) MemoryListResult {
	mapped := MapError(err, correlationID)
	return MemoryListResult{Error: &mapped}
}

func memoryError(err error, correlationID string) MemoryResult {
	mapped := MapError(err, correlationID)
	return MemoryResult{Error: &mapped}
}

func memoryContextError(err error, correlationID string) MemoryContextResult {
	mapped := MapError(err, correlationID)
	return MemoryContextResult{Error: &mapped}
}
