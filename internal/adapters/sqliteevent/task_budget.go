package sqliteevent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/taskbudget"
)

var errTaskBudgetExceeded = errors.New("task budget exceeded")

type taskBudgetCounter struct {
	TaskID, VaultID                           string
	StartedAt, UpdatedAt                      time.Time
	Policy                                    taskbudget.Policy
	ModelCalls, ToolCalls                     int
	InputTokens, OutputTokens                 int
	ReservedInputTokens, ReservedOutputTokens int
}

func reserveModelBudget(ctx context.Context, tx *sql.Tx, vaultID, taskID string, occurredAt time.Time, policy taskbudget.Policy, inputTokens, outputTokens int) error {
	policy, err := taskbudget.Normalize(policy)
	if err != nil || inputTokens < 1 || outputTokens < 256 || inputTokens > policy.MaxInputTokens || outputTokens > policy.MaxOutputTokens {
		return errTaskBudgetExceeded
	}
	counter, err := loadOrCreateTaskBudget(ctx, tx, vaultID, taskID, occurredAt, policy)
	if err != nil {
		return err
	}
	if !sameTaskBudgetPolicy(counter.Policy, policy) || budgetExpired(counter, occurredAt, policy) || counter.ModelCalls >= policy.MaxModelCalls ||
		wouldExceed(counter.InputTokens, counter.ReservedInputTokens, inputTokens, policy.MaxInputTokens) ||
		wouldExceed(counter.OutputTokens, counter.ReservedOutputTokens, outputTokens, policy.MaxOutputTokens) {
		return errTaskBudgetExceeded
	}
	_, err = tx.ExecContext(ctx, `UPDATE task_budget_counters SET model_calls = model_calls + 1, reserved_input_tokens = reserved_input_tokens + ?, reserved_output_tokens = reserved_output_tokens + ?, updated_at = ? WHERE task_id = ? AND vault_id = ?`, inputTokens, outputTokens, occurredAt.Format(time.RFC3339Nano), taskID, vaultID)
	if err != nil {
		return fmt.Errorf("reserve task model budget: %w", err)
	}
	return nil
}

func settleModelBudget(ctx context.Context, tx *sql.Tx, vaultID, taskID string, occurredAt time.Time, policy taskbudget.Policy, reservedInput, reservedOutput, actualInput, actualOutput int) (bool, error) {
	policy, err := taskbudget.Normalize(policy)
	if err != nil || reservedInput < 1 || reservedOutput < 256 || actualInput < 0 || actualOutput < 0 {
		return false, errTaskBudgetExceeded
	}
	counter, err := scanTaskBudget(tx.QueryRowContext(ctx, taskBudgetSelect+" WHERE task_id = ? AND vault_id = ?", taskID, vaultID))
	if err != nil {
		return false, err
	}
	if !sameTaskBudgetPolicy(counter.Policy, policy) || counter.ReservedInputTokens < reservedInput || counter.ReservedOutputTokens < reservedOutput {
		return false, errTaskBudgetExceeded
	}
	exceeded := actualInput > reservedInput || actualOutput > reservedOutput ||
		wouldExceed(counter.InputTokens, 0, actualInput, policy.MaxInputTokens) ||
		wouldExceed(counter.OutputTokens, 0, actualOutput, policy.MaxOutputTokens) ||
		budgetExpired(counter, occurredAt, policy)
	result, err := tx.ExecContext(ctx, `UPDATE task_budget_counters SET input_tokens = input_tokens + ?, output_tokens = output_tokens + ?, reserved_input_tokens = reserved_input_tokens - ?, reserved_output_tokens = reserved_output_tokens - ?, updated_at = ? WHERE task_id = ? AND vault_id = ? AND reserved_input_tokens >= ? AND reserved_output_tokens >= ?`, actualInput, actualOutput, reservedInput, reservedOutput, occurredAt.Format(time.RFC3339Nano), taskID, vaultID, reservedInput, reservedOutput)
	if err != nil {
		return false, fmt.Errorf("settle task model budget: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return false, errTaskBudgetExceeded
	}
	return exceeded, nil
}

func reserveToolBudget(ctx context.Context, tx *sql.Tx, vaultID, taskID string, occurredAt time.Time, policy taskbudget.Policy) error {
	policy, err := taskbudget.Normalize(policy)
	if err != nil {
		return errTaskBudgetExceeded
	}
	counter, err := loadOrCreateTaskBudget(ctx, tx, vaultID, taskID, occurredAt, policy)
	if err != nil {
		return err
	}
	if !sameTaskBudgetPolicy(counter.Policy, policy) || budgetExpired(counter, occurredAt, policy) || counter.ToolCalls >= policy.MaxToolCalls {
		return errTaskBudgetExceeded
	}
	_, err = tx.ExecContext(ctx, `UPDATE task_budget_counters SET tool_calls = tool_calls + 1, updated_at = ? WHERE task_id = ? AND vault_id = ?`, occurredAt.Format(time.RFC3339Nano), taskID, vaultID)
	if err != nil {
		return fmt.Errorf("reserve task tool budget: %w", err)
	}
	return nil
}

func loadOrCreateTaskBudget(ctx context.Context, tx *sql.Tx, vaultID, taskID string, occurredAt time.Time, policy taskbudget.Policy) (taskBudgetCounter, error) {
	counter, err := scanTaskBudget(tx.QueryRowContext(ctx, taskBudgetSelect+" WHERE task_id = ? AND vault_id = ?", taskID, vaultID))
	if err == nil {
		return counter, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return taskBudgetCounter{}, err
	}
	stamp := occurredAt.Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_budget_counters(task_id, vault_id, started_at, max_model_calls, max_tool_calls, max_input_tokens, max_output_tokens, max_wall_clock_ms, model_calls, tool_calls, input_tokens, output_tokens, reserved_input_tokens, reserved_output_tokens, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 0, 0, 0, 0, ?)`, taskID, vaultID, stamp, policy.MaxModelCalls, policy.MaxToolCalls, policy.MaxInputTokens, policy.MaxOutputTokens, policy.MaxWallClock.Milliseconds(), stamp); err != nil {
		return taskBudgetCounter{}, fmt.Errorf("create task budget counter: %w", err)
	}
	return scanTaskBudget(tx.QueryRowContext(ctx, taskBudgetSelect+" WHERE task_id = ? AND vault_id = ?", taskID, vaultID))
}

func scanTaskBudget(row scanner) (taskBudgetCounter, error) {
	var counter taskBudgetCounter
	var startedAt, updatedAt string
	var maxWallClockMS int64
	if err := row.Scan(&counter.TaskID, &counter.VaultID, &startedAt, &counter.Policy.MaxModelCalls, &counter.Policy.MaxToolCalls, &counter.Policy.MaxInputTokens, &counter.Policy.MaxOutputTokens, &maxWallClockMS, &counter.ModelCalls, &counter.ToolCalls, &counter.InputTokens, &counter.OutputTokens, &counter.ReservedInputTokens, &counter.ReservedOutputTokens, &updatedAt); err != nil {
		return taskBudgetCounter{}, err
	}
	counter.Policy.MaxWallClock = time.Duration(maxWallClockMS) * time.Millisecond
	if counter.Policy.Validate() != nil {
		return taskBudgetCounter{}, errTaskBudgetExceeded
	}
	var err error
	if counter.StartedAt, err = time.Parse(time.RFC3339Nano, startedAt); err != nil {
		return taskBudgetCounter{}, fmt.Errorf("parse task budget start: %w", err)
	}
	if counter.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return taskBudgetCounter{}, fmt.Errorf("parse task budget update: %w", err)
	}
	return counter, nil
}

func budgetExpired(counter taskBudgetCounter, occurredAt time.Time, policy taskbudget.Policy) bool {
	return occurredAt.Before(counter.StartedAt) || occurredAt.Sub(counter.StartedAt) >= policy.MaxWallClock
}

func wouldExceed(used, reserved, requested, limit int) bool {
	return int64(used)+int64(reserved)+int64(requested) > int64(limit)
}

func sameTaskBudgetPolicy(left, right taskbudget.Policy) bool {
	return left == right
}

const taskBudgetSelect = `SELECT task_id, vault_id, started_at, max_model_calls, max_tool_calls, max_input_tokens, max_output_tokens, max_wall_clock_ms, model_calls, tool_calls, input_tokens, output_tokens, reserved_input_tokens, reserved_output_tokens, updated_at FROM task_budget_counters`
