package wailsapi

import (
	"context"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
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
	Tasks []TaskDetail `json:"tasks"`
	Error *TalosError  `json:"error,omitempty"`
}

func (s *TaskService) ListContracts(correlationID string) TaskListResult {
	correlationID = normalizeCorrelationID(correlationID)
	fail := func(err error) TaskListResult {
		mapped := MapError(err, correlationID)
		return TaskListResult{Tasks: []TaskDetail{}, Error: &mapped}
	}
	if s == nil || s.vault == nil || s.workspace == nil {
		return fail(taskstore.ErrInvalidCommand)
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
	records, err := discovery.ListTaskContracts(lease.Context, taskstore.ListInput{VaultID: vault.ID, WorkspaceRoot: snapshot.Root, BaselineCommit: snapshot.BaselineCommit, Limit: 50})
	if err != nil {
		return fail(err)
	}
	result := TaskListResult{Tasks: make([]TaskDetail, 0, len(records))}
	for _, record := range records {
		if record.Task.VaultID != vault.ID || record.Task.WorkspaceRoot != snapshot.Root || record.Task.BaselineCommit != snapshot.BaselineCommit {
			return fail(taskstore.ErrInvalidCommand)
		}
		contract := record.Contract
		commands := make([]VerificationCommandRequest, 0, len(contract.VerificationCommands))
		for _, command := range contract.VerificationCommands {
			commands = append(commands, VerificationCommandRequest{RuleID: command.RuleID, Arguments: append([]string{}, command.Arguments...), WorkingDirectory: command.WorkingDirectory})
		}
		result.Tasks = append(result.Tasks, TaskDetail{Task: taskStatus(record), Goal: contract.Goal, AllowedPaths: append([]string{}, contract.AllowedPaths...), ForbiddenActions: append([]string{}, contract.ForbiddenActions...), AcceptanceCriteria: append([]string{}, contract.AcceptanceCriteria...), VerificationCommands: commands})
	}
	return result
}
