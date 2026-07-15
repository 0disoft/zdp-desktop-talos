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
	"github.com/0disoft/zdp-desktop-talos/internal/domain/patchaction"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/id"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/patchstore"
)

const patchActionEventSchemaVersion = 1

type patchActionPayload struct {
	ActionID          string            `json:"action_id"`
	TaskID            string            `json:"task_id"`
	Kind              patchaction.Kind  `json:"kind"`
	State             patchaction.State `json:"state"`
	ContractRevision  int               `json:"contract_revision"`
	PatchHash         string            `json:"patch_hash"`
	WorktreeStateHash string            `json:"worktree_state_hash"`
	EvidenceID        string            `json:"evidence_id,omitempty"`
	SafeErrorCode     string            `json:"safe_error_code,omitempty"`
	OccurredAt        string            `json:"occurred_at"`
}

func (s *Store) PreparePatchAction(ctx context.Context, input patchstore.PrepareInput) (patchstore.Prepared, error) {
	now := normalizedTime(input.OccurredAt, s.now)
	if input.VaultID == "" || input.TaskID == "" || input.ContractRevision < 1 || len(input.PatchHash) != 64 || len(input.WorktreeStateHash) != 64 || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 || (input.Kind != patchaction.KindApply && input.Kind != patchaction.KindDiscard) || (input.Kind == patchaction.KindApply && input.EvidenceID == "") {
		return patchstore.Prepared{}, patchstore.ErrInvalidCommand
	}
	hash, err := patchCommandHash(input)
	if err != nil {
		return patchstore.Prepared{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return patchstore.Prepared{}, fmt.Errorf("begin patch action: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existing, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, hash)
	if err != nil {
		return patchstore.Prepared{}, mapPatchIdempotencyError(err)
	}
	if found {
		action, err := scanPatchAction(tx.QueryRowContext(ctx, patchActionSelect+" WHERE created_event_id = ?", existing.ID))
		return patchstore.Prepared{Action: action, Replayed: true}, err
	}
	if err := requireActiveVault(ctx, tx, input.VaultID); err != nil {
		return patchstore.Prepared{}, err
	}
	pointer, err := scanTaskPointer(tx.QueryRowContext(ctx, taskSelect+" WHERE task_id = ? AND vault_id = ?", input.TaskID, input.VaultID))
	if err != nil || task.Status(pointer.status) != task.StatusContracted || pointer.currentRevision != input.ContractRevision {
		return patchstore.Prepared{}, patchstore.ErrConflict
	}
	var outcomeCount, unresolvedCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_outcomes WHERE task_id = ?`, input.TaskID).Scan(&outcomeCount); err != nil || outcomeCount != 0 {
		return patchstore.Prepared{}, patchstore.ErrConflict
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM patch_actions WHERE task_id = ? AND state IN ('pending','unknown')`, input.TaskID).Scan(&unresolvedCount); err != nil || unresolvedCount != 0 {
		return patchstore.Prepared{}, patchstore.ErrConflict
	}
	if input.Kind == patchaction.KindApply {
		var evidenceTask, evidenceVault, stateHash string
		var evidenceRevision int
		if err := tx.QueryRowContext(ctx, `SELECT task_id, vault_id, contract_revision, worktree_state_hash FROM verification_evidence WHERE evidence_id = ?`, input.EvidenceID).Scan(&evidenceTask, &evidenceVault, &evidenceRevision, &stateHash); err != nil || evidenceTask != input.TaskID || evidenceVault != input.VaultID || evidenceRevision != input.ContractRevision || stateHash != input.WorktreeStateHash {
			return patchstore.Prepared{}, patchstore.ErrConflict
		}
		var blockers int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM decisions WHERE vault_id = ? AND task_id = ? AND category = 'blocking' AND state != 'answered'`, input.VaultID, input.TaskID).Scan(&blockers); err != nil {
			return patchstore.Prepared{}, err
		}
		if blockers != 0 {
			return patchstore.Prepared{}, patchstore.ErrBlockingDecision
		}
	}
	actionID, err := id.UUIDv7(now, s.random)
	if err != nil {
		return patchstore.Prepared{}, err
	}
	payload := patchActionPayload{ActionID: actionID, TaskID: input.TaskID, Kind: input.Kind, State: patchaction.StatePending, ContractRevision: input.ContractRevision, PatchHash: input.PatchHash, WorktreeStateHash: input.WorktreeStateHash, EvidenceID: input.EvidenceID, OccurredAt: now.Format(time.RFC3339Nano)}
	record, err := s.patchActionEvent(input.VaultID, "patch.action.prepared", payload, now)
	if err != nil {
		return patchstore.Prepared{}, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return patchstore.Prepared{}, err
	}
	var evidenceID any
	if input.EvidenceID != "" {
		evidenceID = input.EvidenceID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO patch_actions(action_id, vault_id, task_id, kind, state, contract_revision, patch_hash, worktree_state_hash, evidence_id, safe_error_code, created_at, updated_at, created_event_id, last_event_id) VALUES(?, ?, ?, ?, 'pending', ?, ?, ?, ?, '', ?, ?, ?, ?)`, actionID, input.VaultID, input.TaskID, string(input.Kind), input.ContractRevision, input.PatchHash, input.WorktreeStateHash, evidenceID, payload.OccurredAt, payload.OccurredAt, record.ID, record.ID); err != nil {
		return patchstore.Prepared{}, fmt.Errorf("insert patch action: %w", err)
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, record.ID, hash); err != nil {
		return patchstore.Prepared{}, err
	}
	if err := tx.Commit(); err != nil {
		return patchstore.Prepared{}, err
	}
	action, err := scanPatchAction(s.db.QueryRowContext(ctx, patchActionSelect+" WHERE action_id = ?", actionID))
	return patchstore.Prepared{Action: action}, err
}

func (s *Store) FinishPatchAction(ctx context.Context, input patchstore.FinishInput) (patchaction.Record, error) {
	now := normalizedTime(input.OccurredAt, s.now)
	if input.VaultID == "" || input.ActionID == "" || input.ExpectedState != patchaction.StatePending || (input.NextState != patchaction.StateSucceeded && input.NextState != patchaction.StateFailed && input.NextState != patchaction.StateUnknown) || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 || ((input.NextState == patchaction.StateFailed || input.NextState == patchaction.StateUnknown) == (input.SafeErrorCode == "")) {
		return patchaction.Record{}, patchstore.ErrInvalidCommand
	}
	hash, err := patchCommandHash(input)
	if err != nil {
		return patchaction.Record{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return patchaction.Record{}, err
	}
	defer func() { _ = tx.Rollback() }()
	existing, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, hash)
	if err != nil {
		return patchaction.Record{}, mapPatchIdempotencyError(err)
	}
	if found {
		return scanPatchAction(tx.QueryRowContext(ctx, patchActionSelect+" WHERE last_event_id = ?", existing.ID))
	}
	current, err := scanPatchAction(tx.QueryRowContext(ctx, patchActionSelect+" WHERE action_id = ? AND vault_id = ?", input.ActionID, input.VaultID))
	if err != nil || current.State != input.ExpectedState {
		return patchaction.Record{}, patchstore.ErrConflict
	}
	payload := patchActionPayload{ActionID: current.ID, TaskID: current.TaskID, Kind: current.Kind, State: input.NextState, ContractRevision: current.ContractRevision, PatchHash: current.PatchHash, WorktreeStateHash: current.WorktreeStateHash, EvidenceID: current.EvidenceID, SafeErrorCode: input.SafeErrorCode, OccurredAt: now.Format(time.RFC3339Nano)}
	record, err := s.patchActionEvent(input.VaultID, "patch.action.finished", payload, now)
	if err != nil {
		return patchaction.Record{}, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return patchaction.Record{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE patch_actions SET state = ?, safe_error_code = ?, updated_at = ?, last_event_id = ? WHERE action_id = ? AND state = 'pending'`, string(input.NextState), input.SafeErrorCode, payload.OccurredAt, record.ID, current.ID)
	if err != nil {
		return patchaction.Record{}, err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return patchaction.Record{}, patchstore.ErrConflict
	}
	if input.NextState == patchaction.StateSucceeded {
		outcome := task.StatusCompleted
		if current.Kind == patchaction.KindDiscard {
			outcome = task.StatusDiscarded
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_outcomes(task_id, status, action_id, completed_at, event_id) VALUES(?, ?, ?, ?, ?)`, current.TaskID, string(outcome), current.ID, payload.OccurredAt, record.ID); err != nil {
			return patchaction.Record{}, patchstore.ErrConflict
		}
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, record.ID, hash); err != nil {
		return patchaction.Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return patchaction.Record{}, err
	}
	return scanPatchAction(s.db.QueryRowContext(ctx, patchActionSelect+" WHERE action_id = ?", current.ID))
}

const patchActionSelect = `SELECT action_id, vault_id, task_id, kind, state, contract_revision, patch_hash, worktree_state_hash, COALESCE(evidence_id, ''), safe_error_code, created_at, updated_at, created_event_id, last_event_id FROM patch_actions`

func scanPatchAction(row scanner) (patchaction.Record, error) {
	var record patchaction.Record
	var createdAt, updatedAt string
	if err := row.Scan(&record.ID, &record.VaultID, &record.TaskID, &record.Kind, &record.State, &record.ContractRevision, &record.PatchHash, &record.WorktreeStateHash, &record.EvidenceID, &record.SafeErrorCode, &createdAt, &updatedAt, &record.CreatedEventID, &record.LastEventID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return patchaction.Record{}, patchstore.ErrNotFound
		}
		return patchaction.Record{}, err
	}
	record.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	record.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	if err := record.Validate(); err != nil {
		return patchaction.Record{}, patchstore.ErrConflict
	}
	return record, nil
}

func (s *Store) patchActionEvent(vaultID, eventType string, payload patchActionPayload, occurredAt time.Time) (event.Record, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return event.Record{}, err
	}
	return s.newEventRecord(vaultID, eventType, patchActionEventSchemaVersion, event.SensitivityPrivate, encoded, occurredAt)
}

func patchCommandHash(input any) ([]byte, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	return hash[:], nil
}

func mapPatchIdempotencyError(err error) error {
	if errors.Is(err, ErrIdempotencyConflict) || errors.Is(err, ErrIdempotencyUnverifiable) {
		return patchstore.ErrIdempotencyConflict
	}
	return err
}
