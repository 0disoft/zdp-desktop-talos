package wailsapi

import (
	"context"
	"strings"

	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
)

type TaskCreateRequest struct {
	Goal               string   `json:"goal"`
	AllowedPaths       []string `json:"allowed_paths"`
	ForbiddenActions   []string `json:"forbidden_actions"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Risk               string   `json:"risk"`
	RequestID          string   `json:"request_id"`
	CorrelationID      string   `json:"correlation_id"`
}

type TaskReviseRequest struct {
	TaskID             string   `json:"task_id"`
	ExpectedRevision   int      `json:"expected_revision"`
	Goal               string   `json:"goal"`
	AllowedPaths       []string `json:"allowed_paths"`
	ForbiddenActions   []string `json:"forbidden_actions"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Risk               string   `json:"risk"`
	RequestID          string   `json:"request_id"`
	CorrelationID      string   `json:"correlation_id"`
}

type TaskStatus struct {
	TaskID         string `json:"task_id"`
	Revision       int    `json:"revision"`
	BaselineCommit string `json:"baseline_commit"`
	Risk           string `json:"risk"`
	Status         string `json:"status"`
	CreatedAt      string `json:"created_at"`
}

type TaskResult struct {
	Task  *TaskStatus `json:"task,omitempty"`
	Error *TalosError `json:"error,omitempty"`
}

type TaskService struct {
	vault     *VaultService
	workspace *WorkspaceService
}

func NewTaskService(vault *VaultService, workspace *WorkspaceService) *TaskService {
	return &TaskService{vault: vault, workspace: workspace}
}

func (s *TaskService) CreateContract(request TaskCreateRequest) TaskResult {
	correlationID := normalizeCorrelationID(request.CorrelationID)
	if s.vault == nil || s.workspace == nil || strings.TrimSpace(request.RequestID) == "" {
		mapped := MapError(taskstore.ErrInvalidCommand, correlationID)
		return TaskResult{Error: &mapped}
	}
	snapshot, err := s.workspace.contractSnapshot()
	if err != nil {
		mapped := MapError(err, correlationID)
		return TaskResult{Error: &mapped}
	}
	created, err := s.vault.createTaskContract(vaultbootstrap.CreateTaskContractInput{
		WorkspaceRoot: snapshot.Root, BaselineCommit: snapshot.BaselineCommit, Goal: request.Goal,
		AllowedPaths: request.AllowedPaths, ForbiddenActions: request.ForbiddenActions,
		AcceptanceCriteria: request.AcceptanceCriteria, Risk: task.Risk(request.Risk),
		IdempotencyKey: "task-contract:" + strings.TrimSpace(request.RequestID),
	})
	if err != nil {
		mapped := MapError(err, correlationID)
		return TaskResult{Error: &mapped}
	}
	status := taskStatus(created)
	return TaskResult{Task: &status}
}

func (s *TaskService) ReviseContract(request TaskReviseRequest) TaskResult {
	correlationID := normalizeCorrelationID(request.CorrelationID)
	if s.vault == nil || s.workspace == nil || strings.TrimSpace(request.RequestID) == "" || strings.TrimSpace(request.TaskID) == "" || request.ExpectedRevision < 1 {
		mapped := MapError(taskstore.ErrInvalidCommand, correlationID)
		return TaskResult{Error: &mapped}
	}
	snapshot, err := s.workspace.contractSnapshot()
	if err != nil {
		mapped := MapError(err, correlationID)
		return TaskResult{Error: &mapped}
	}
	revised, err := s.vault.reviseTaskContract(vaultbootstrap.ReviseTaskContractInput{
		TaskID: strings.TrimSpace(request.TaskID), ExpectedRevision: request.ExpectedRevision,
		WorkspaceRoot: snapshot.Root, BaselineCommit: snapshot.BaselineCommit, Goal: request.Goal,
		AllowedPaths: request.AllowedPaths, ForbiddenActions: request.ForbiddenActions,
		AcceptanceCriteria: request.AcceptanceCriteria, Risk: task.Risk(request.Risk),
		IdempotencyKey: "task-contract-revision:" + strings.TrimSpace(request.RequestID),
	})
	if err != nil {
		mapped := MapError(err, correlationID)
		return TaskResult{Error: &mapped}
	}
	status := taskStatus(revised)
	return TaskResult{Task: &status}
}

func taskStatus(created taskstore.Created) TaskStatus {
	return TaskStatus{
		TaskID: created.Task.ID, Revision: created.Contract.Revision, BaselineCommit: created.Task.BaselineCommit,
		Risk: string(created.Contract.Risk), Status: string(created.Task.Status), CreatedAt: created.Task.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
	}
}

func (s *VaultService) createTaskContract(input vaultbootstrap.CreateTaskContractInput) (taskstore.Created, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		return taskstore.Created{}, vaultbootstrap.ErrNotOpen
	}
	return s.session.CreateTaskContract(context.Background(), input)
}

func (s *VaultService) reviseTaskContract(input vaultbootstrap.ReviseTaskContractInput) (taskstore.Created, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		return taskstore.Created{}, vaultbootstrap.ErrNotOpen
	}
	return s.session.ReviseTaskContract(context.Background(), input)
}
