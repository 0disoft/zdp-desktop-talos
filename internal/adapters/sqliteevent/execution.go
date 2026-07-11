package sqliteevent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/execution"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/permission"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/executionstore"
)

const executionEventSchemaVersion = 1

type executionPayload struct {
	GrantID        string                 `json:"grant_id,omitempty"`
	Outcome        permission.Outcome     `json:"outcome,omitempty"`
	TaskID         string                 `json:"task_id,omitempty"`
	WorkspaceHash  string                 `json:"workspace_hash,omitempty"`
	CapabilityHash string                 `json:"capability_hash,omitempty"`
	RunID          string                 `json:"run_id,omitempty"`
	AttemptID      string                 `json:"attempt_id,omitempty"`
	CallID         string                 `json:"call_id,omitempty"`
	RunState       execution.RunState     `json:"run_state,omitempty"`
	AttemptState   execution.AttemptState `json:"attempt_state,omitempty"`
	ExitCode       *int                   `json:"exit_code,omitempty"`
	SafeErrorCode  string                 `json:"safe_error_code,omitempty"`
	OccurredAt     string                 `json:"occurred_at"`
}

func (s *Store) SavePermissionGrant(ctx context.Context, input executionstore.SaveGrantInput) (permission.Grant, error) {
	occurredAt := normalizedTime(input.OccurredAt, s.now)
	grant := input.Grant
	if grant.CreatedAt.IsZero() {
		grant.CreatedAt = occurredAt
	}
	if input.VaultID == "" || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 || grant.Validate() != nil {
		return permission.Grant{}, executionstore.ErrInvalidCommand
	}
	requestHash, err := hashExecutionCommand(input)
	if err != nil {
		return permission.Grant{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return permission.Grant{}, fmt.Errorf("begin permission grant transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existing, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, requestHash)
	if err != nil {
		return permission.Grant{}, mapExecutionIdempotencyError(err)
	}
	if found {
		return scanPermissionGrant(tx.QueryRowContext(ctx, permissionGrantSelect+" WHERE created_event_id = ?", existing.ID))
	}
	if err := requireActiveVault(ctx, tx, input.VaultID); err != nil {
		return permission.Grant{}, err
	}
	payload := executionPayload{GrantID: grant.ID, Outcome: grant.Outcome, TaskID: grant.TaskID, WorkspaceHash: grant.WorkspaceHash, CapabilityHash: grant.CapabilityHash, OccurredAt: occurredAt.Format(time.RFC3339Nano)}
	record, err := s.executionEvent(input.VaultID, "permission.grant.created", payload, occurredAt)
	if err != nil {
		return permission.Grant{}, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return permission.Grant{}, err
	}
	var taskID any
	if grant.TaskID != "" {
		taskID = grant.TaskID
	}
	var expiresAt any
	if !grant.ExpiresAt.IsZero() {
		expiresAt = grant.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO permission_grants(grant_id, vault_id, outcome, state, capability_hash, task_id, workspace_hash, created_at, expires_at, created_event_id, last_event_id) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, grant.ID, input.VaultID, string(grant.Outcome), string(grant.State), grant.CapabilityHash, taskID, grant.WorkspaceHash, grant.CreatedAt.UTC().Format(time.RFC3339Nano), expiresAt, record.ID, record.ID); err != nil {
		return permission.Grant{}, fmt.Errorf("insert permission grant: %w", err)
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, record.ID, requestHash); err != nil {
		return permission.Grant{}, err
	}
	if err := tx.Commit(); err != nil {
		return permission.Grant{}, fmt.Errorf("commit permission grant: %w", err)
	}
	return grant, nil
}

func (s *Store) ListActivePermissionGrants(ctx context.Context, vaultID, workspaceHash string) ([]permission.Grant, error) {
	if vaultID == "" || len(workspaceHash) != 64 {
		return nil, executionstore.ErrInvalidCommand
	}
	rows, err := s.db.QueryContext(ctx, permissionGrantSelect+" WHERE vault_id = ? AND workspace_hash = ? AND state = 'active' ORDER BY created_at, grant_id", vaultID, workspaceHash)
	if err != nil {
		return nil, fmt.Errorf("list permission grants: %w", err)
	}
	defer rows.Close()
	grants := make([]permission.Grant, 0)
	for rows.Next() {
		grant, err := scanPermissionGrant(rows)
		if err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate permission grants: %w", err)
	}
	return grants, nil
}

func (s *Store) PrepareAttempt(ctx context.Context, input executionstore.PrepareAttemptInput) (executionstore.Prepared, error) {
	occurredAt := normalizedTime(input.OccurredAt, s.now)
	if input.VaultID == "" || input.TaskID == "" || input.RunID == "" || input.AttemptID == "" || input.CallID == "" || len(input.WorkspaceHash) != 64 || len(input.CapabilityHash) != 64 || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 {
		return executionstore.Prepared{}, executionstore.ErrInvalidCommand
	}
	requestHash, err := hashExecutionCommand(input)
	if err != nil {
		return executionstore.Prepared{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return executionstore.Prepared{}, fmt.Errorf("begin attempt preparation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existing, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, requestHash)
	if err != nil {
		return executionstore.Prepared{}, mapExecutionIdempotencyError(err)
	}
	if found {
		return s.preparedByEvent(ctx, tx, existing.ID)
	}
	if err := requireActiveVault(ctx, tx, input.VaultID); err != nil {
		return executionstore.Prepared{}, err
	}
	pointer, err := scanTaskPointer(tx.QueryRowContext(ctx, taskSelect+" WHERE task_id = ? AND vault_id = ?", input.TaskID, input.VaultID))
	if err != nil {
		return executionstore.Prepared{}, executionstore.ErrNotFound
	}
	taskEvent, err := s.getEventInTx(tx, pointer.lastEventID)
	if err != nil {
		return executionstore.Prepared{}, err
	}
	taskRecord, err := taskFromEvent(taskEvent, pointer)
	if err != nil {
		return executionstore.Prepared{}, err
	}
	authoritativeWorkspaceHash, err := permission.WorkspaceHash(taskRecord.WorkspaceRoot)
	if err != nil || authoritativeWorkspaceHash != input.WorkspaceHash {
		return executionstore.Prepared{}, executionstore.ErrConflict
	}
	consumeGrant := false
	if input.GrantID != "" {
		grant, err := scanPermissionGrant(tx.QueryRowContext(ctx, permissionGrantSelect+" WHERE grant_id = ? AND vault_id = ?", input.GrantID, input.VaultID))
		if err != nil || !grantAllowsExecution(grant.Outcome) || grant.State != permission.GrantActive || grant.CapabilityHash != input.CapabilityHash || grant.WorkspaceHash != input.WorkspaceHash || (grant.TaskID != "" && grant.TaskID != input.TaskID) || occurredAt.Before(grant.CreatedAt) || (!grant.ExpiresAt.IsZero() && !grant.ExpiresAt.After(occurredAt)) {
			return executionstore.Prepared{}, executionstore.ErrGrantUnavailable
		}
		consumeGrant = grant.Outcome == permission.OutcomeAllowOnce
	}
	payload := executionPayload{GrantID: input.GrantID, TaskID: input.TaskID, WorkspaceHash: input.WorkspaceHash, CapabilityHash: input.CapabilityHash, RunID: input.RunID, AttemptID: input.AttemptID, CallID: input.CallID, AttemptState: execution.AttemptDispatchPending, OccurredAt: occurredAt.Format(time.RFC3339Nano)}
	record, err := s.executionEvent(input.VaultID, "execution.attempt.prepared", payload, occurredAt)
	if err != nil {
		return executionstore.Prepared{}, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return executionstore.Prepared{}, err
	}
	if consumeGrant {
		result, err := tx.ExecContext(ctx, `UPDATE permission_grants SET state = 'consumed', last_event_id = ? WHERE grant_id = ? AND state = 'active'`, record.ID, input.GrantID)
		if err != nil {
			return executionstore.Prepared{}, fmt.Errorf("consume one-time permission grant: %w", err)
		}
		rows, _ := result.RowsAffected()
		if rows != 1 {
			return executionstore.Prepared{}, executionstore.ErrGrantUnavailable
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO runs(run_id, vault_id, task_id, workspace_hash, state, created_at, updated_at, created_event_id, last_event_id) VALUES(?, ?, ?, ?, 'active', ?, ?, ?, ?) ON CONFLICT(run_id) DO NOTHING`, input.RunID, input.VaultID, input.TaskID, input.WorkspaceHash, payload.OccurredAt, payload.OccurredAt, record.ID, record.ID); err != nil {
		return executionstore.Prepared{}, fmt.Errorf("insert active run: %w", err)
	}
	run, err := scanRun(tx.QueryRowContext(ctx, runSelect+" WHERE run_id = ?", input.RunID))
	if err != nil || run.State != execution.RunActive || run.TaskID != input.TaskID || run.WorkspaceHash != input.WorkspaceHash {
		return executionstore.Prepared{}, executionstore.ErrConflict
	}
	var grantID any
	if input.GrantID != "" {
		grantID = input.GrantID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO attempts(attempt_id, run_id, call_id, capability_hash, grant_id, state, created_at, updated_at, prepared_event_id, last_event_id) VALUES(?, ?, ?, ?, ?, 'dispatch_pending', ?, ?, ?, ?)`, input.AttemptID, input.RunID, input.CallID, input.CapabilityHash, grantID, payload.OccurredAt, payload.OccurredAt, record.ID, record.ID); err != nil {
		return executionstore.Prepared{}, fmt.Errorf("insert pending attempt: %w", err)
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, record.ID, requestHash); err != nil {
		return executionstore.Prepared{}, err
	}
	if err := tx.Commit(); err != nil {
		return executionstore.Prepared{}, fmt.Errorf("commit attempt preparation: %w", err)
	}
	attempt, err := s.GetAttempt(ctx, input.AttemptID)
	return executionstore.Prepared{Run: run, Attempt: attempt}, err
}

func grantAllowsExecution(outcome permission.Outcome) bool {
	return outcome == permission.OutcomeAllowOnce || outcome == permission.OutcomeAllowTask || outcome == permission.OutcomeAllowWorkspace
}

func (s *Store) FinishAttempt(ctx context.Context, input executionstore.FinishAttemptInput) (execution.Attempt, error) {
	occurredAt := normalizedTime(input.OccurredAt, s.now)
	validation := execution.Attempt{ID: input.AttemptID, RunID: "validation", CallID: "validation", CapabilityHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", State: input.NextState, ExitCode: input.ExitCode, SafeErrorCode: input.SafeErrorCode, CreatedAt: occurredAt, UpdatedAt: occurredAt, LastEventID: "validation"}
	if input.VaultID == "" || input.AttemptID == "" || input.ExpectedState != execution.AttemptDispatchPending || input.IdempotencyKey == "" || validation.Validate() != nil {
		return execution.Attempt{}, executionstore.ErrInvalidCommand
	}
	requestHash, err := hashExecutionCommand(input)
	if err != nil {
		return execution.Attempt{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return execution.Attempt{}, fmt.Errorf("begin attempt finish: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existing, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, requestHash)
	if err != nil {
		return execution.Attempt{}, mapExecutionIdempotencyError(err)
	}
	if found {
		return scanAttempt(tx.QueryRowContext(ctx, attemptSelect+" WHERE last_event_id = ?", existing.ID))
	}
	current, err := scanAttempt(tx.QueryRowContext(ctx, attemptSelect+" WHERE attempt_id = ?", input.AttemptID))
	if err != nil {
		return execution.Attempt{}, err
	}
	if occurredAt.Before(current.CreatedAt) {
		return execution.Attempt{}, executionstore.ErrInvalidCommand
	}
	var runVault string
	if err := tx.QueryRowContext(ctx, `SELECT vault_id FROM runs WHERE run_id = ?`, current.RunID).Scan(&runVault); err != nil || runVault != input.VaultID {
		return execution.Attempt{}, executionstore.ErrConflict
	}
	payload := executionPayload{RunID: current.RunID, AttemptID: current.ID, AttemptState: input.NextState, ExitCode: input.ExitCode, SafeErrorCode: input.SafeErrorCode, OccurredAt: occurredAt.Format(time.RFC3339Nano)}
	record, err := s.executionEvent(input.VaultID, "execution.attempt.finished", payload, occurredAt)
	if err != nil {
		return execution.Attempt{}, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return execution.Attempt{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE attempts SET state = ?, exit_code = ?, safe_error_code = ?, updated_at = ?, last_event_id = ? WHERE attempt_id = ? AND state = ?`, string(input.NextState), input.ExitCode, input.SafeErrorCode, payload.OccurredAt, record.ID, input.AttemptID, string(input.ExpectedState))
	if err != nil {
		return execution.Attempt{}, fmt.Errorf("finish attempt: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return execution.Attempt{}, executionstore.ErrConflict
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, record.ID, requestHash); err != nil {
		return execution.Attempt{}, err
	}
	if err := tx.Commit(); err != nil {
		return execution.Attempt{}, fmt.Errorf("commit attempt finish: %w", err)
	}
	return s.GetAttempt(ctx, input.AttemptID)
}

func (s *Store) FinishRun(ctx context.Context, input executionstore.FinishRunInput) (execution.Run, error) {
	occurredAt := normalizedTime(input.OccurredAt, s.now)
	if input.VaultID == "" || input.RunID == "" || input.ExpectedState != execution.RunActive || input.NextState == execution.RunActive || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 {
		return execution.Run{}, executionstore.ErrInvalidCommand
	}
	requestHash, err := hashExecutionCommand(input)
	if err != nil {
		return execution.Run{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return execution.Run{}, fmt.Errorf("begin run finish: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existing, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, requestHash)
	if err != nil {
		return execution.Run{}, mapExecutionIdempotencyError(err)
	}
	if found {
		return scanRun(tx.QueryRowContext(ctx, runSelect+" WHERE last_event_id = ?", existing.ID))
	}
	current, err := scanRun(tx.QueryRowContext(ctx, runSelect+" WHERE run_id = ? AND vault_id = ?", input.RunID, input.VaultID))
	if err != nil {
		return execution.Run{}, err
	}
	validation := current
	validation.State = input.NextState
	validation.UpdatedAt = occurredAt
	if validation.Validate() != nil {
		return execution.Run{}, executionstore.ErrInvalidCommand
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempts WHERE run_id = ? AND state = 'dispatch_pending'`, input.RunID).Scan(&pending); err != nil {
		return execution.Run{}, fmt.Errorf("count pending run attempts: %w", err)
	}
	if pending != 0 {
		return execution.Run{}, executionstore.ErrConflict
	}
	payload := executionPayload{RunID: input.RunID, RunState: input.NextState, OccurredAt: occurredAt.Format(time.RFC3339Nano)}
	record, err := s.executionEvent(input.VaultID, "execution.run.finished", payload, occurredAt)
	if err != nil {
		return execution.Run{}, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return execution.Run{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE runs SET state = ?, updated_at = ?, last_event_id = ? WHERE run_id = ? AND vault_id = ? AND state = ?`, string(input.NextState), payload.OccurredAt, record.ID, input.RunID, input.VaultID, string(input.ExpectedState))
	if err != nil {
		return execution.Run{}, fmt.Errorf("finish run: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return execution.Run{}, executionstore.ErrConflict
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, record.ID, requestHash); err != nil {
		return execution.Run{}, err
	}
	if err := tx.Commit(); err != nil {
		return execution.Run{}, fmt.Errorf("commit run finish: %w", err)
	}
	return scanRun(s.db.QueryRowContext(ctx, runSelect+" WHERE run_id = ?", input.RunID))
}

func (s *Store) ReconcilePendingAttempts(ctx context.Context, vaultID string, occurredAt time.Time) (int, error) {
	if vaultID == "" || occurredAt.IsZero() {
		return 0, executionstore.ErrInvalidCommand
	}
	occurredAt = occurredAt.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin pending attempt reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT a.attempt_id, a.run_id FROM attempts a JOIN runs r ON r.run_id = a.run_id WHERE r.vault_id = ? AND a.state = 'dispatch_pending' ORDER BY a.created_at, a.attempt_id`, vaultID)
	if err != nil {
		return 0, fmt.Errorf("list pending attempts for reconciliation: %w", err)
	}
	type pendingAttempt struct{ attemptID, runID string }
	pending := make([]pendingAttempt, 0)
	for rows.Next() {
		var item pendingAttempt
		if err := rows.Scan(&item.attemptID, &item.runID); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan pending attempt for reconciliation: %w", err)
		}
		pending = append(pending, item)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close pending attempt reconciliation rows: %w", err)
	}
	for _, item := range pending {
		current, err := scanAttempt(tx.QueryRowContext(ctx, attemptSelect+" WHERE attempt_id = ?", item.attemptID))
		if err != nil || occurredAt.Before(current.CreatedAt) {
			return 0, executionstore.ErrInvalidCommand
		}
		payload := executionPayload{RunID: item.runID, AttemptID: item.attemptID, AttemptState: execution.AttemptUnknown, SafeErrorCode: "WORKER_OUTCOME_UNKNOWN", OccurredAt: occurredAt.Format(time.RFC3339Nano)}
		record, err := s.executionEvent(vaultID, "execution.attempt.reconciled", payload, occurredAt)
		if err != nil {
			return 0, err
		}
		if err := s.insertEvent(ctx, tx, record); err != nil {
			return 0, err
		}
		result, err := tx.ExecContext(ctx, `UPDATE attempts SET state = 'unknown', safe_error_code = 'WORKER_OUTCOME_UNKNOWN', updated_at = ?, last_event_id = ? WHERE attempt_id = ? AND state = 'dispatch_pending'`, payload.OccurredAt, record.ID, item.attemptID)
		if err != nil {
			return 0, fmt.Errorf("reconcile pending attempt: %w", err)
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			return 0, executionstore.ErrConflict
		}
		result, err = tx.ExecContext(ctx, `UPDATE runs SET state = 'unknown', updated_at = ?, last_event_id = ? WHERE run_id = ? AND state = 'active'`, payload.OccurredAt, record.ID, item.runID)
		if err != nil {
			return 0, fmt.Errorf("reconcile active run: %w", err)
		}
		count, _ = result.RowsAffected()
		if count != 1 {
			return 0, executionstore.ErrConflict
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit pending attempt reconciliation: %w", err)
	}
	return len(pending), nil
}

func (s *Store) GetAttempt(ctx context.Context, attemptID string) (execution.Attempt, error) {
	return scanAttempt(s.db.QueryRowContext(ctx, attemptSelect+" WHERE attempt_id = ?", attemptID))
}

func (s *Store) executionEvent(vaultID, eventType string, payload executionPayload, occurredAt time.Time) (event.Record, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return event.Record{}, fmt.Errorf("encode execution event: %w", err)
	}
	return s.newEventRecord(vaultID, eventType, executionEventSchemaVersion, event.SensitivityPrivate, encoded, occurredAt)
}

func (s *Store) preparedByEvent(ctx context.Context, tx *sql.Tx, eventID string) (executionstore.Prepared, error) {
	var attemptID string
	if err := tx.QueryRowContext(ctx, `SELECT attempt_id FROM attempts WHERE prepared_event_id = ?`, eventID).Scan(&attemptID); err != nil {
		return executionstore.Prepared{}, executionstore.ErrConflict
	}
	attempt, err := scanAttempt(tx.QueryRowContext(ctx, attemptSelect+" WHERE attempt_id = ?", attemptID))
	if err != nil {
		return executionstore.Prepared{}, err
	}
	run, err := scanRun(tx.QueryRowContext(ctx, runSelect+" WHERE run_id = ?", attempt.RunID))
	return executionstore.Prepared{Run: run, Attempt: attempt}, err
}

func hashExecutionCommand(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode execution command: %w", err)
	}
	hash := sha256.Sum256(encoded)
	return hash[:], nil
}

func mapExecutionIdempotencyError(err error) error {
	if errors.Is(err, ErrIdempotencyConflict) || errors.Is(err, ErrIdempotencyUnverifiable) {
		return executionstore.ErrIdempotencyConflict
	}
	return err
}

func normalizedTime(value time.Time, now func() time.Time) time.Time {
	if value.IsZero() {
		value = now()
	}
	return value.UTC()
}

const permissionGrantSelect = `SELECT grant_id, outcome, state, capability_hash, task_id, workspace_hash, created_at, expires_at FROM permission_grants`
const runSelect = `SELECT run_id, vault_id, task_id, workspace_hash, state, created_at, updated_at, last_event_id FROM runs`
const attemptSelect = `SELECT attempt_id, run_id, call_id, capability_hash, grant_id, state, exit_code, safe_error_code, created_at, updated_at, last_event_id FROM attempts`

func scanPermissionGrant(row scanner) (permission.Grant, error) {
	var grant permission.Grant
	var outcome, state, createdAt string
	var taskID, expiresAt sql.NullString
	if err := row.Scan(&grant.ID, &outcome, &state, &grant.CapabilityHash, &taskID, &grant.WorkspaceHash, &createdAt, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return permission.Grant{}, executionstore.ErrNotFound
		}
		return permission.Grant{}, fmt.Errorf("scan permission grant: %w", err)
	}
	grant.Outcome, grant.State, grant.TaskID = permission.Outcome(outcome), permission.GrantState(state), taskID.String
	var err error
	grant.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err == nil && expiresAt.Valid {
		grant.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiresAt.String)
	}
	if err != nil || grant.Validate() != nil {
		return permission.Grant{}, executionstore.ErrConflict
	}
	return grant, nil
}

func scanRun(row scanner) (execution.Run, error) {
	var run execution.Run
	var state, createdAt, updatedAt string
	if err := row.Scan(&run.ID, &run.VaultID, &run.TaskID, &run.WorkspaceHash, &state, &createdAt, &updatedAt, &run.LastEventID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return execution.Run{}, executionstore.ErrNotFound
		}
		return execution.Run{}, fmt.Errorf("scan execution run: %w", err)
	}
	run.State = execution.RunState(state)
	run.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	run.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	if run.Validate() != nil {
		return execution.Run{}, executionstore.ErrConflict
	}
	return run, nil
}

func scanAttempt(row scanner) (execution.Attempt, error) {
	var attempt execution.Attempt
	var state, createdAt, updatedAt string
	var grantID sql.NullString
	var exitCode sql.NullInt64
	if err := row.Scan(&attempt.ID, &attempt.RunID, &attempt.CallID, &attempt.CapabilityHash, &grantID, &state, &exitCode, &attempt.SafeErrorCode, &createdAt, &updatedAt, &attempt.LastEventID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return execution.Attempt{}, executionstore.ErrNotFound
		}
		return execution.Attempt{}, fmt.Errorf("scan execution attempt: %w", err)
	}
	attempt.GrantID, attempt.State = grantID.String, execution.AttemptState(state)
	if exitCode.Valid {
		value := int(exitCode.Int64)
		attempt.ExitCode = &value
	}
	attempt.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	attempt.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	if attempt.Validate() != nil {
		return execution.Attempt{}, executionstore.ErrConflict
	}
	return attempt, nil
}
