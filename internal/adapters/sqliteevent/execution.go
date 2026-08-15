package sqliteevent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/execution"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/permission"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/verification"
	"github.com/0disoft/zdp-desktop-talos/internal/id"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/executionstore"
)

const executionEventSchemaVersion = 1

type executionPayload struct {
	GrantID           string                 `json:"grant_id,omitempty"`
	Outcome           permission.Outcome     `json:"outcome,omitempty"`
	TaskID            string                 `json:"task_id,omitempty"`
	WorkspaceHash     string                 `json:"workspace_hash,omitempty"`
	CapabilityHash    string                 `json:"capability_hash,omitempty"`
	RunID             string                 `json:"run_id,omitempty"`
	AttemptID         string                 `json:"attempt_id,omitempty"`
	CallID            string                 `json:"call_id,omitempty"`
	RunState          execution.RunState     `json:"run_state,omitempty"`
	AttemptState      execution.AttemptState `json:"attempt_state,omitempty"`
	ExitCode          *int                   `json:"exit_code,omitempty"`
	SafeErrorCode     string                 `json:"safe_error_code,omitempty"`
	EvidenceID        string                 `json:"evidence_id,omitempty"`
	ContractRevision  int                    `json:"contract_revision,omitempty"`
	CommandIndex      int                    `json:"command_index,omitempty"`
	BaselineCommit    string                 `json:"baseline_commit,omitempty"`
	WorktreeStateHash string                 `json:"worktree_state_hash,omitempty"`
	StartedAt         string                 `json:"started_at,omitempty"`
	FinishedAt        string                 `json:"finished_at,omitempty"`
	OccurredAt        string                 `json:"occurred_at"`
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
	if input.VaultID == "" || input.TaskID == "" || len(input.WorkspaceHash) != 64 || len(input.CapabilityHash) != 64 || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 {
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
	runID, err := id.UUIDv7(occurredAt, s.random)
	if err != nil {
		return executionstore.Prepared{}, fmt.Errorf("generate run id: %w", err)
	}
	attemptID, err := id.UUIDv7(occurredAt, s.random)
	if err != nil {
		return executionstore.Prepared{}, fmt.Errorf("generate attempt id: %w", err)
	}
	callID, err := id.UUIDv7(occurredAt, s.random)
	if err != nil {
		return executionstore.Prepared{}, fmt.Errorf("generate tool call id: %w", err)
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
	storedTask, err := taskFromEvent(taskEvent, pointer)
	if err != nil {
		return executionstore.Prepared{}, err
	}
	taskRecord, err := s.resolveTaskWorkspace(ctx, tx, storedTask)
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
	if err := reserveToolBudget(ctx, tx, input.VaultID, input.TaskID, occurredAt, input.Budget); err != nil {
		if errors.Is(err, errTaskBudgetExceeded) {
			return executionstore.Prepared{}, executionstore.ErrBudgetExceeded
		}
		return executionstore.Prepared{}, err
	}
	payload := executionPayload{GrantID: input.GrantID, TaskID: input.TaskID, WorkspaceHash: input.WorkspaceHash, CapabilityHash: input.CapabilityHash, RunID: runID, AttemptID: attemptID, CallID: callID, AttemptState: execution.AttemptDispatchPending, OccurredAt: occurredAt.Format(time.RFC3339Nano)}
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO runs(run_id, vault_id, task_id, workspace_hash, state, created_at, updated_at, created_event_id, last_event_id) VALUES(?, ?, ?, ?, 'active', ?, ?, ?, ?)`, runID, input.VaultID, input.TaskID, input.WorkspaceHash, payload.OccurredAt, payload.OccurredAt, record.ID, record.ID); err != nil {
		return executionstore.Prepared{}, fmt.Errorf("insert active run: %w", err)
	}
	run, err := scanRun(tx.QueryRowContext(ctx, runSelect+" WHERE run_id = ?", runID))
	if err != nil {
		return executionstore.Prepared{}, err
	}
	var grantID any
	if input.GrantID != "" {
		grantID = input.GrantID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO attempts(attempt_id, run_id, call_id, capability_hash, grant_id, state, created_at, updated_at, prepared_event_id, last_event_id) VALUES(?, ?, ?, ?, ?, 'dispatch_pending', ?, ?, ?, ?)`, attemptID, runID, callID, input.CapabilityHash, grantID, payload.OccurredAt, payload.OccurredAt, record.ID, record.ID); err != nil {
		return executionstore.Prepared{}, fmt.Errorf("insert pending attempt: %w", err)
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, record.ID, requestHash); err != nil {
		return executionstore.Prepared{}, err
	}
	if err := tx.Commit(); err != nil {
		return executionstore.Prepared{}, fmt.Errorf("commit attempt preparation: %w", err)
	}
	attempt, err := s.GetAttempt(ctx, attemptID)
	return executionstore.Prepared{Run: run, Attempt: attempt}, err
}

func grantAllowsExecution(outcome permission.Outcome) bool {
	return outcome == permission.OutcomeAllowOnce || outcome == permission.OutcomeAllowTask || outcome == permission.OutcomeAllowWorkspace
}

func (s *Store) FinishExecution(ctx context.Context, input executionstore.FinishExecutionInput) (executionstore.FinishedExecution, error) {
	attemptInput, runInput := input.Attempt, input.Run
	occurredAt := normalizedTime(attemptInput.OccurredAt, s.now)
	attemptValidation := execution.Attempt{ID: attemptInput.AttemptID, RunID: runInput.RunID, CallID: "validation", CapabilityHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", State: attemptInput.NextState, ExitCode: attemptInput.ExitCode, SafeErrorCode: attemptInput.SafeErrorCode, CreatedAt: occurredAt, UpdatedAt: occurredAt, LastEventID: "validation"}
	if attemptInput.VaultID == "" || attemptInput.VaultID != runInput.VaultID || attemptInput.AttemptID == "" || runInput.RunID == "" || attemptInput.ExpectedState != execution.AttemptDispatchPending || runInput.ExpectedState != execution.RunActive || runInput.NextState == execution.RunActive || attemptInput.IdempotencyKey == "" || attemptInput.IdempotencyKey != runInput.IdempotencyKey || len(attemptInput.IdempotencyKey) > 128 || !runInput.OccurredAt.Equal(attemptInput.OccurredAt) || attemptValidation.Validate() != nil || (attemptInput.NextState == execution.AttemptSucceeded) != (attemptInput.Evidence != nil) {
		return executionstore.FinishedExecution{}, executionstore.ErrInvalidCommand
	}
	requestHash, err := hashExecutionCommand(input)
	if err != nil {
		return executionstore.FinishedExecution{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return executionstore.FinishedExecution{}, fmt.Errorf("begin execution finish: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existing, found, err := s.resolveIdempotency(ctx, tx, attemptInput.IdempotencyKey, requestHash)
	if err != nil {
		return executionstore.FinishedExecution{}, mapExecutionIdempotencyError(err)
	}
	if found {
		finished, err := finishedAttemptByEvent(tx, existing.ID)
		if err != nil {
			return executionstore.FinishedExecution{}, err
		}
		run, err := scanRun(tx.QueryRowContext(ctx, runSelect+" WHERE run_id = ? AND vault_id = ?", finished.Attempt.RunID, attemptInput.VaultID))
		return executionstore.FinishedExecution{Attempt: finished.Attempt, Run: run, Evidence: finished.Evidence}, err
	}
	currentAttempt, err := scanAttempt(tx.QueryRowContext(ctx, attemptSelect+" WHERE attempt_id = ?", attemptInput.AttemptID))
	if err != nil || currentAttempt.RunID != runInput.RunID || occurredAt.Before(currentAttempt.CreatedAt) {
		return executionstore.FinishedExecution{}, executionstore.ErrConflict
	}
	currentRun, err := scanRun(tx.QueryRowContext(ctx, runSelect+" WHERE run_id = ? AND vault_id = ?", runInput.RunID, runInput.VaultID))
	if err != nil {
		return executionstore.FinishedExecution{}, err
	}
	runValidation := currentRun
	runValidation.State = runInput.NextState
	runValidation.UpdatedAt = occurredAt
	if runValidation.Validate() != nil {
		return executionstore.FinishedExecution{}, executionstore.ErrInvalidCommand
	}
	attemptPayload := executionPayload{RunID: currentAttempt.RunID, AttemptID: currentAttempt.ID, AttemptState: attemptInput.NextState, ExitCode: attemptInput.ExitCode, SafeErrorCode: attemptInput.SafeErrorCode, OccurredAt: occurredAt.Format(time.RFC3339Nano)}
	var evidenceRecord *verification.Evidence
	if attemptInput.Evidence != nil {
		candidate, err := s.prepareVerificationEvidence(ctx, tx, attemptInput.VaultID, currentRun, currentAttempt, *attemptInput.Evidence, occurredAt)
		if err != nil {
			return executionstore.FinishedExecution{}, err
		}
		evidenceRecord = &candidate
		attemptPayload.EvidenceID, attemptPayload.ContractRevision, attemptPayload.CommandIndex = candidate.ID, candidate.ContractRevision, candidate.CommandIndex
		attemptPayload.BaselineCommit, attemptPayload.WorktreeStateHash = candidate.BaselineCommit, candidate.WorktreeStateHash
		attemptPayload.CapabilityHash = candidate.CapabilityHash
		attemptPayload.StartedAt, attemptPayload.FinishedAt = candidate.StartedAt.Format(time.RFC3339Nano), candidate.FinishedAt.Format(time.RFC3339Nano)
	}
	attemptEvent, err := s.executionEvent(attemptInput.VaultID, "execution.attempt.finished", attemptPayload, occurredAt)
	if err != nil {
		return executionstore.FinishedExecution{}, err
	}
	if err := s.insertEvent(ctx, tx, attemptEvent); err != nil {
		return executionstore.FinishedExecution{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE attempts SET state = ?, exit_code = ?, safe_error_code = ?, updated_at = ?, last_event_id = ? WHERE attempt_id = ? AND state = ?`, string(attemptInput.NextState), attemptInput.ExitCode, attemptInput.SafeErrorCode, attemptPayload.OccurredAt, attemptEvent.ID, attemptInput.AttemptID, string(attemptInput.ExpectedState))
	if err != nil {
		return executionstore.FinishedExecution{}, fmt.Errorf("finish execution attempt: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return executionstore.FinishedExecution{}, executionstore.ErrConflict
	}
	if evidenceRecord != nil {
		evidenceRecord.EventID = attemptEvent.ID
		if err := insertVerificationEvidence(ctx, tx, *evidenceRecord); err != nil {
			return executionstore.FinishedExecution{}, err
		}
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempts WHERE run_id = ? AND state = 'dispatch_pending'`, runInput.RunID).Scan(&pending); err != nil {
		return executionstore.FinishedExecution{}, fmt.Errorf("count pending execution attempts: %w", err)
	}
	if pending != 0 {
		return executionstore.FinishedExecution{}, executionstore.ErrConflict
	}
	runPayload := executionPayload{RunID: runInput.RunID, RunState: runInput.NextState, OccurredAt: occurredAt.Format(time.RFC3339Nano)}
	runEvent, err := s.executionEvent(runInput.VaultID, "execution.run.finished", runPayload, occurredAt)
	if err != nil {
		return executionstore.FinishedExecution{}, err
	}
	if err := s.insertEvent(ctx, tx, runEvent); err != nil {
		return executionstore.FinishedExecution{}, err
	}
	result, err = tx.ExecContext(ctx, `UPDATE runs SET state = ?, updated_at = ?, last_event_id = ? WHERE run_id = ? AND vault_id = ? AND state = ?`, string(runInput.NextState), runPayload.OccurredAt, runEvent.ID, runInput.RunID, runInput.VaultID, string(runInput.ExpectedState))
	if err != nil {
		return executionstore.FinishedExecution{}, fmt.Errorf("finish execution run: %w", err)
	}
	rows, _ = result.RowsAffected()
	if rows != 1 {
		return executionstore.FinishedExecution{}, executionstore.ErrConflict
	}
	if err := claimIdempotency(ctx, tx, attemptInput.IdempotencyKey, attemptEvent.ID, requestHash); err != nil {
		return executionstore.FinishedExecution{}, err
	}
	if err := tx.Commit(); err != nil {
		return executionstore.FinishedExecution{}, fmt.Errorf("commit execution finish: %w", err)
	}
	attempt, err := s.GetAttempt(ctx, attemptInput.AttemptID)
	if err != nil {
		return executionstore.FinishedExecution{}, err
	}
	run, err := scanRun(s.db.QueryRowContext(ctx, runSelect+" WHERE run_id = ?", runInput.RunID))
	return executionstore.FinishedExecution{Attempt: attempt, Run: run, Evidence: evidenceRecord}, err
}

func (s *Store) FinishAttempt(ctx context.Context, input executionstore.FinishAttemptInput) (executionstore.FinishedAttempt, error) {
	occurredAt := normalizedTime(input.OccurredAt, s.now)
	validation := execution.Attempt{ID: input.AttemptID, RunID: "validation", CallID: "validation", CapabilityHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", State: input.NextState, ExitCode: input.ExitCode, SafeErrorCode: input.SafeErrorCode, CreatedAt: occurredAt, UpdatedAt: occurredAt, LastEventID: "validation"}
	if input.VaultID == "" || input.AttemptID == "" || input.ExpectedState != execution.AttemptDispatchPending || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 || validation.Validate() != nil || (input.NextState == execution.AttemptSucceeded) != (input.Evidence != nil) {
		return executionstore.FinishedAttempt{}, executionstore.ErrInvalidCommand
	}
	requestHash, err := hashExecutionCommand(input)
	if err != nil {
		return executionstore.FinishedAttempt{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return executionstore.FinishedAttempt{}, fmt.Errorf("begin attempt finish: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existing, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, requestHash)
	if err != nil {
		return executionstore.FinishedAttempt{}, mapExecutionIdempotencyError(err)
	}
	if found {
		return finishedAttemptByEvent(tx, existing.ID)
	}
	current, err := scanAttempt(tx.QueryRowContext(ctx, attemptSelect+" WHERE attempt_id = ?", input.AttemptID))
	if err != nil {
		return executionstore.FinishedAttempt{}, err
	}
	if occurredAt.Before(current.CreatedAt) {
		return executionstore.FinishedAttempt{}, executionstore.ErrInvalidCommand
	}
	run, err := scanRun(tx.QueryRowContext(ctx, runSelect+" WHERE run_id = ?", current.RunID))
	if err != nil || run.VaultID != input.VaultID {
		return executionstore.FinishedAttempt{}, executionstore.ErrConflict
	}
	payload := executionPayload{RunID: current.RunID, AttemptID: current.ID, AttemptState: input.NextState, ExitCode: input.ExitCode, SafeErrorCode: input.SafeErrorCode, OccurredAt: occurredAt.Format(time.RFC3339Nano)}
	var evidenceRecord *verification.Evidence
	if input.Evidence != nil {
		candidate, err := s.prepareVerificationEvidence(ctx, tx, input.VaultID, run, current, *input.Evidence, occurredAt)
		if err != nil {
			return executionstore.FinishedAttempt{}, err
		}
		evidenceRecord = &candidate
		payload.EvidenceID, payload.ContractRevision, payload.CommandIndex = candidate.ID, candidate.ContractRevision, candidate.CommandIndex
		payload.BaselineCommit, payload.WorktreeStateHash = candidate.BaselineCommit, candidate.WorktreeStateHash
		payload.CapabilityHash = candidate.CapabilityHash
		payload.StartedAt, payload.FinishedAt = candidate.StartedAt.Format(time.RFC3339Nano), candidate.FinishedAt.Format(time.RFC3339Nano)
	}
	record, err := s.executionEvent(input.VaultID, "execution.attempt.finished", payload, occurredAt)
	if err != nil {
		return executionstore.FinishedAttempt{}, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return executionstore.FinishedAttempt{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE attempts SET state = ?, exit_code = ?, safe_error_code = ?, updated_at = ?, last_event_id = ? WHERE attempt_id = ? AND state = ?`, string(input.NextState), input.ExitCode, input.SafeErrorCode, payload.OccurredAt, record.ID, input.AttemptID, string(input.ExpectedState))
	if err != nil {
		return executionstore.FinishedAttempt{}, fmt.Errorf("finish attempt: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return executionstore.FinishedAttempt{}, executionstore.ErrConflict
	}
	if evidenceRecord != nil {
		evidenceRecord.EventID = record.ID
		if err := insertVerificationEvidence(ctx, tx, *evidenceRecord); err != nil {
			return executionstore.FinishedAttempt{}, err
		}
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, record.ID, requestHash); err != nil {
		return executionstore.FinishedAttempt{}, err
	}
	if err := tx.Commit(); err != nil {
		return executionstore.FinishedAttempt{}, fmt.Errorf("commit attempt finish: %w", err)
	}
	attempt, err := s.GetAttempt(ctx, input.AttemptID)
	return executionstore.FinishedAttempt{Attempt: attempt, Evidence: evidenceRecord}, err
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
	if err != nil {
		return executionstore.Prepared{}, err
	}
	prepared := executionstore.Prepared{Run: run, Attempt: attempt, Replayed: true}
	if attempt.State == execution.AttemptSucceeded {
		evidence, evidenceErr := scanVerificationEvidence(tx.QueryRowContext(ctx, verificationEvidenceSelect+" WHERE attempt_id = ?", attempt.ID))
		if evidenceErr != nil {
			return executionstore.Prepared{}, executionstore.ErrConflict
		}
		prepared.Evidence = &evidence
	}
	return prepared, nil
}

func (s *Store) prepareVerificationEvidence(ctx context.Context, tx *sql.Tx, vaultID string, run execution.Run, attempt execution.Attempt, input executionstore.VerificationEvidenceInput, occurredAt time.Time) (verification.Evidence, error) {
	var currentRevision int
	var baselineCommit string
	if err := tx.QueryRowContext(ctx, `SELECT current_revision, baseline_commit FROM tasks WHERE task_id = ? AND vault_id = ?`, run.TaskID, vaultID).Scan(&currentRevision, &baselineCommit); err != nil {
		return verification.Evidence{}, executionstore.ErrConflict
	}
	if input.TaskID != run.TaskID || input.ContractRevision != currentRevision || input.BaselineCommit != baselineCommit || input.CapabilityHash != attempt.CapabilityHash || input.StartedAt.Before(attempt.CreatedAt) || input.FinishedAt.Before(input.StartedAt) || occurredAt.Before(input.FinishedAt) {
		return verification.Evidence{}, executionstore.ErrConflict
	}
	evidenceID, err := id.UUIDv7(occurredAt, s.random)
	if err != nil {
		return verification.Evidence{}, fmt.Errorf("generate verification evidence id: %w", err)
	}
	record := verification.Evidence{
		ID: evidenceID, VaultID: vaultID, TaskID: run.TaskID, RunID: run.ID, AttemptID: attempt.ID,
		ContractRevision: input.ContractRevision, CommandIndex: input.CommandIndex, BaselineCommit: input.BaselineCommit,
		WorktreeStateHash: input.WorktreeStateHash, CapabilityHash: input.CapabilityHash, ExitCode: 0,
		StartedAt: input.StartedAt.UTC(), FinishedAt: input.FinishedAt.UTC(), EventID: "pending-event",
	}
	if err := record.Validate(); err != nil {
		return verification.Evidence{}, executionstore.ErrInvalidCommand
	}
	return record, nil
}

func insertVerificationEvidence(ctx context.Context, tx *sql.Tx, evidence verification.Evidence) error {
	if err := evidence.Validate(); err != nil {
		return executionstore.ErrInvalidCommand
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO verification_evidence(
		evidence_id, vault_id, task_id, run_id, attempt_id, contract_revision, command_index, baseline_commit,
		worktree_state_hash, capability_hash, exit_code, started_at, finished_at, event_id
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, evidence.ID, evidence.VaultID, evidence.TaskID,
		evidence.RunID, evidence.AttemptID, evidence.ContractRevision, evidence.CommandIndex, evidence.BaselineCommit,
		evidence.WorktreeStateHash, evidence.CapabilityHash, evidence.ExitCode, evidence.StartedAt.Format(time.RFC3339Nano),
		evidence.FinishedAt.Format(time.RFC3339Nano), evidence.EventID)
	if err != nil {
		return fmt.Errorf("insert verification evidence: %w", err)
	}
	return nil
}

func finishedAttemptByEvent(tx *sql.Tx, eventID string) (executionstore.FinishedAttempt, error) {
	attempt, err := scanAttempt(tx.QueryRow(attemptSelect+" WHERE last_event_id = ?", eventID))
	if err != nil {
		return executionstore.FinishedAttempt{}, err
	}
	evidence, err := scanVerificationEvidence(tx.QueryRow(verificationEvidenceSelect+" WHERE event_id = ?", eventID))
	if errors.Is(err, executionstore.ErrNotFound) {
		return executionstore.FinishedAttempt{Attempt: attempt}, nil
	}
	if err != nil {
		return executionstore.FinishedAttempt{}, err
	}
	return executionstore.FinishedAttempt{Attempt: attempt, Evidence: &evidence}, nil
}

const verificationEvidenceSelect = `SELECT evidence_id, vault_id, task_id, run_id, attempt_id, contract_revision,
	command_index, baseline_commit, worktree_state_hash, capability_hash, exit_code, started_at, finished_at, event_id FROM verification_evidence`

func (s *Store) GetLatestVerificationEvidence(ctx context.Context, vaultID, taskID string) (verification.Evidence, error) {
	if strings.TrimSpace(vaultID) == "" || strings.TrimSpace(taskID) == "" {
		return verification.Evidence{}, executionstore.ErrInvalidCommand
	}
	return scanVerificationEvidence(s.db.QueryRowContext(ctx, verificationEvidenceSelect+
		" WHERE vault_id = ? AND task_id = ? ORDER BY finished_at DESC, evidence_id DESC LIMIT 1", vaultID, taskID))
}

func scanVerificationEvidence(row scanner) (verification.Evidence, error) {
	var evidence verification.Evidence
	var startedAt, finishedAt string
	if err := row.Scan(&evidence.ID, &evidence.VaultID, &evidence.TaskID, &evidence.RunID, &evidence.AttemptID,
		&evidence.ContractRevision, &evidence.CommandIndex, &evidence.BaselineCommit, &evidence.WorktreeStateHash,
		&evidence.CapabilityHash, &evidence.ExitCode, &startedAt, &finishedAt, &evidence.EventID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return verification.Evidence{}, executionstore.ErrNotFound
		}
		return verification.Evidence{}, fmt.Errorf("scan verification evidence: %w", err)
	}
	evidence.StartedAt, _ = time.Parse(time.RFC3339Nano, startedAt)
	evidence.FinishedAt, _ = time.Parse(time.RFC3339Nano, finishedAt)
	if evidence.Validate() != nil {
		return verification.Evidence{}, executionstore.ErrConflict
	}
	return evidence, nil
}

func hashExecutionCommand(value any) ([]byte, error) {
	switch input := value.(type) {
	case executionstore.SaveGrantInput:
		input.OccurredAt = time.Time{}
		value = input
	case executionstore.PrepareAttemptInput:
		input.OccurredAt = time.Time{}
		value = input
	case executionstore.FinishAttemptInput:
		input.OccurredAt = time.Time{}
		value = input
	case executionstore.FinishRunInput:
		input.OccurredAt = time.Time{}
		value = input
	case executionstore.FinishExecutionInput:
		input.Attempt.OccurredAt = time.Time{}
		input.Run.OccurredAt = time.Time{}
		value = input
	}
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
