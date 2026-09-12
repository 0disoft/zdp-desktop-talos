package wailsapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
	"strings"
	"time"
)

type TaskDetail struct {
	Task                 TaskStatus                   `json:"task"`
	Goal                 string                       `json:"goal"`
	AllowedPaths         []string                     `json:"allowed_paths"`
	ForbiddenActions     []string                     `json:"forbidden_actions"`
	AcceptanceCriteria   []string                     `json:"acceptance_criteria"`
	VerificationCommands []VerificationCommandRequest `json:"verification_commands"`
}

type TaskListResult struct {
	Tasks      []TaskDetail `json:"tasks"`
	Error      *TalosError  `json:"error,omitempty"`
	NextCursor string       `json:"next_cursor,omitempty"`
	Scanned    int          `json:"scanned,omitempty"`
}

type TaskPageRequest struct {
	Cursor        string `json:"cursor"`
	Status        string `json:"status"`
	Query         string `json:"query"`
	AllBaselines  bool   `json:"all_baselines"`
	CorrelationID string `json:"correlation_id"`
}

type taskCursor struct {
	At string `json:"at"`
	ID string `json:"id"`
}

func (s *TaskService) ListContracts(correlationID string) TaskListResult {
	return s.listContracts(TaskPageRequest{CorrelationID: correlationID}, false)
}

func (s *TaskService) PageContracts(request TaskPageRequest) TaskListResult {
	return s.listContracts(request, true)
}

func (s *TaskService) listContracts(request TaskPageRequest, paged bool) TaskListResult {
	correlationID := request.CorrelationID
	correlationID = normalizeCorrelationID(correlationID)
	fail := func(err error) TaskListResult {
		mapped := MapError(err, correlationID)
		return TaskListResult{Tasks: []TaskDetail{}, Error: &mapped}
	}
	if s == nil || s.vault == nil || s.workspace == nil {
		return fail(taskstore.ErrInvalidCommand)
	}
	var cursor taskCursor
	if len(request.Query) > 240 || len(request.Cursor) > 512 || (request.Status != "" && request.Status != "contracted" && request.Status != "completed" && request.Status != "discarded") {
		return fail(taskstore.ErrInvalidCommand)
	}
	if request.Cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(request.Cursor)
		if err != nil || json.Unmarshal(decoded, &cursor) != nil || cursor.ID == "" || len(cursor.ID) > 128 {
			return fail(taskstore.ErrInvalidCommand)
		}
		if _, err := time.Parse(time.RFC3339Nano, cursor.At); err != nil {
			return fail(taskstore.ErrInvalidCommand)
		}
	}
	snapshot, err := s.workspace.decisionSnapshot()
	if err != nil {
		return fail(err)
	}
	lease, err := s.vault.acquireSessionLease(context.Background())
	if err != nil {
		return fail(err)
	}
	defer lease.Release()
	vault, err := lease.Session.CurrentVault()
	if err != nil {
		return fail(err)
	}
	database, err := lease.Session.ExecutionDatabase()
	if err != nil {
		return fail(err)
	}
	discovery, ok := database.(taskstore.Discovery)
	if !ok {
		return fail(taskstore.ErrInvalidCommand)
	}
	records, err := discovery.ListTaskContracts(lease.Context, taskstore.ListInput{VaultID: vault.ID, WorkspaceRoot: snapshot.Root, BaselineCommit: snapshot.BaselineCommit, Limit: 50, Paged: paged, BeforeCreatedAt: cursor.At, BeforeID: cursor.ID, Status: request.Status, AllBaselines: request.AllBaselines})
	if err != nil {
		return fail(err)
	}
	result := TaskListResult{Tasks: make([]TaskDetail, 0, len(records))}
	if paged {
		result.Scanned = len(records)
		if len(records) == 50 {
			last := records[len(records)-1].Task
			encoded, _ := json.Marshal(taskCursor{At: last.CreatedAt.UTC().Format(time.RFC3339Nano), ID: last.ID})
			result.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
		}
	}
	for _, record := range records {
		if record.Task.VaultID != vault.ID || record.Task.WorkspaceRoot != snapshot.Root || (!request.AllBaselines && record.Task.BaselineCommit != snapshot.BaselineCommit) {
			return fail(taskstore.ErrInvalidCommand)
		}
		contract := record.Contract
		if request.Query != "" && !strings.Contains(strings.ToLower(contract.Goal), strings.ToLower(strings.TrimSpace(request.Query))) {
			continue
		}
		commands := make([]VerificationCommandRequest, 0, len(contract.VerificationCommands))
		for _, command := range contract.VerificationCommands {
			commands = append(commands, VerificationCommandRequest{RuleID: command.RuleID, Arguments: append([]string{}, command.Arguments...), WorkingDirectory: command.WorkingDirectory})
		}
		result.Tasks = append(result.Tasks, TaskDetail{Task: taskStatus(record), Goal: contract.Goal, AllowedPaths: append([]string{}, contract.AllowedPaths...), ForbiddenActions: append([]string{}, contract.ForbiddenActions...), AcceptanceCriteria: append([]string{}, contract.AcceptanceCriteria...), VerificationCommands: commands})
	}
	return result
}
