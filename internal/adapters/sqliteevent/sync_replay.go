package sqliteevent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/decision"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/syncstate"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/decisionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorystore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/syncstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
)

var syncableEventSchemas = map[string]int{
	taskContractCreatedEventType:    taskContractEventSchemaVersion,
	taskContractRevisedEventType:    taskContractEventSchemaVersion,
	taskContractSnapshotEventType:   taskContractEventSchemaVersion,
	decisionCreatedEventType:        decisionEventSchemaVersion,
	decisionAnsweredEventType:       decisionEventSchemaVersion,
	decisionSupersededEventType:     decisionEventSchemaVersion,
	decisionResolvedEventType:       decisionEventSchemaVersion,
	memoryCandidateCreatedEventType: memoryEventSchemaVersion,
	memoryStateChangedEventType:     memoryEventSchemaVersion,
	memorySnapshotEventType:         memoryEventSchemaVersion,
	syncDeviceRevocationEventType:   syncDeviceRevocationSchemaVersion,
}

type syncReplayPayload struct {
	PackID           string `json:"pack_id"`
	VaultID          string `json:"vault_id"`
	DeviceID         string `json:"device_id"`
	SequenceStart    uint64 `json:"sequence_start"`
	SequenceEnd      uint64 `json:"sequence_end"`
	EventCount       int    `json:"event_count"`
	AppliedCount     int    `json:"applied_count"`
	ConflictedCount  int    `json:"conflicted_count"`
	QuarantinedCount int    `json:"quarantined_count"`
	CompletedAt      string `json:"completed_at"`
}

func (s *Store) ApplyValidatedSyncPack(ctx context.Context, input syncstore.ApplyReplayInput) (syncstate.ReplayResult, bool, error) {
	if ctx == nil || input.PackID == "" || input.VaultID == "" || input.DeviceID == "" || len(input.Events) == 0 || len(input.Events) > syncstate.MaxEventsPerPack {
		return syncstate.ReplayResult{}, false, syncstore.ErrInvalidCommand
	}
	if input.CompletedAt.IsZero() {
		input.CompletedAt = s.now()
	}
	input.CompletedAt = input.CompletedAt.UTC()
	for index, candidate := range input.Events {
		if candidate.DeviceSeq < 1 || candidate.Record.VaultID != input.VaultID || candidate.Record.Validate() != nil || index > 0 && candidate.DeviceSeq != input.Events[index-1].DeviceSeq+1 {
			return syncstate.ReplayResult{}, false, syncstore.ErrInvalidCommand
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return syncstate.ReplayResult{}, false, fmt.Errorf("begin sync replay: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if existing, found, err := s.getSyncReplay(ctx, tx, input.VaultID, input.PackID); err != nil {
		return syncstate.ReplayResult{}, false, err
	} else if found {
		if !sameReplayInput(existing, input) {
			return syncstate.ReplayResult{}, false, syncstore.ErrReplayConflict
		}
		return existing, true, nil
	}
	receipt, _, found, err := s.getValidatedSyncPack(ctx, tx, input.VaultID, input.PackID)
	if err != nil {
		return syncstate.ReplayResult{}, false, err
	}
	if !found || receipt.DeviceID != input.DeviceID || receipt.EventCount != len(input.Events) || receipt.SequenceStart != input.Events[0].DeviceSeq || receipt.SequenceEnd != input.Events[len(input.Events)-1].DeviceSeq || input.CompletedAt.Before(receipt.ReceivedAt) {
		return syncstate.ReplayResult{}, false, syncstore.ErrReplayConflict
	}

	items := make([]syncstate.ReplayItem, 0, len(input.Events))
	counts := map[syncstate.ReplayState]int{}
	for _, candidate := range input.Events {
		state, reason, err := s.replayOneEvent(ctx, tx, input, candidate)
		if err != nil {
			return syncstate.ReplayResult{}, false, err
		}
		item := syncstate.ReplayItem{PackID: input.PackID, VaultID: input.VaultID, DeviceID: input.DeviceID, DeviceSeq: candidate.DeviceSeq, EventID: candidate.Record.ID, EventHash: replayEventHash(candidate.Record), State: state, ReasonCode: reason, RecordedAt: input.CompletedAt}
		if item.Validate() != nil {
			return syncstate.ReplayResult{}, false, syncstore.ErrReplayConflict
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO sync_replay_items(pack_id, vault_id, device_id, device_seq, event_id, event_hash, state, reason_code, recorded_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`, item.PackID, item.VaultID, item.DeviceID, item.DeviceSeq, item.EventID, item.EventHash, string(item.State), item.ReasonCode, item.RecordedAt.Format(time.RFC3339Nano)); err != nil {
			return syncstate.ReplayResult{}, false, fmt.Errorf("insert sync replay item: %w", err)
		}
		items = append(items, item)
		counts[state]++
	}

	payload := syncReplayPayload{PackID: input.PackID, VaultID: input.VaultID, DeviceID: input.DeviceID, SequenceStart: receipt.SequenceStart, SequenceEnd: receipt.SequenceEnd, EventCount: receipt.EventCount, AppliedCount: counts[syncstate.ReplayApplied], ConflictedCount: counts[syncstate.ReplayConflicted], QuarantinedCount: counts[syncstate.ReplayQuarantined], CompletedAt: input.CompletedAt.Format(time.RFC3339Nano)}
	audit, err := s.syncEvent("sync.pack.replayed", input.VaultID, payload, input.CompletedAt)
	if err != nil {
		return syncstate.ReplayResult{}, false, err
	}
	if err := s.insertEvent(ctx, tx, audit); err != nil {
		return syncstate.ReplayResult{}, false, err
	}
	batch := syncstate.ReplayBatch{PackID: input.PackID, VaultID: input.VaultID, DeviceID: input.DeviceID, SequenceStart: receipt.SequenceStart, SequenceEnd: receipt.SequenceEnd, EventCount: receipt.EventCount, AppliedCount: payload.AppliedCount, ConflictedCount: payload.ConflictedCount, QuarantinedCount: payload.QuarantinedCount, CompletedAt: input.CompletedAt, LastEventID: audit.ID}
	if batch.Validate() != nil {
		return syncstate.ReplayResult{}, false, syncstore.ErrReplayConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_replay_batches(pack_id, vault_id, device_id, sequence_start, sequence_end, event_count, applied_count, conflicted_count, quarantined_count, completed_at, event_id) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, batch.PackID, batch.VaultID, batch.DeviceID, batch.SequenceStart, batch.SequenceEnd, batch.EventCount, batch.AppliedCount, batch.ConflictedCount, batch.QuarantinedCount, batch.CompletedAt.Format(time.RFC3339Nano), batch.LastEventID); err != nil {
		return syncstate.ReplayResult{}, false, fmt.Errorf("insert sync replay batch: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return syncstate.ReplayResult{}, false, fmt.Errorf("commit sync replay: %w", err)
	}
	return syncstate.ReplayResult{Batch: batch, Items: items}, false, nil
}

func (s *Store) GetSyncReplay(ctx context.Context, vaultID, packID string) (syncstate.ReplayResult, error) {
	result, found, err := s.getSyncReplay(ctx, s.db, vaultID, packID)
	if err != nil {
		return syncstate.ReplayResult{}, err
	}
	if !found {
		return syncstate.ReplayResult{}, syncstore.ErrReplayNotFound
	}
	return result, nil
}

func (s *Store) getSyncReplay(ctx context.Context, queryer syncReadQueryer, vaultID, packID string) (syncstate.ReplayResult, bool, error) {
	var batch syncstate.ReplayBatch
	var sequenceStart, sequenceEnd int64
	var completedAt string
	err := queryer.QueryRowContext(ctx, `SELECT pack_id, vault_id, device_id, sequence_start, sequence_end, event_count, applied_count, conflicted_count, quarantined_count, completed_at, event_id FROM sync_replay_batches WHERE vault_id = ? AND pack_id = ?`, vaultID, packID).Scan(&batch.PackID, &batch.VaultID, &batch.DeviceID, &sequenceStart, &sequenceEnd, &batch.EventCount, &batch.AppliedCount, &batch.ConflictedCount, &batch.QuarantinedCount, &completedAt, &batch.LastEventID)
	if errors.Is(err, sql.ErrNoRows) {
		return syncstate.ReplayResult{}, false, nil
	}
	if err != nil {
		return syncstate.ReplayResult{}, false, err
	}
	batch.SequenceStart, batch.SequenceEnd = uint64(sequenceStart), uint64(sequenceEnd)
	batch.CompletedAt, _ = time.Parse(time.RFC3339Nano, completedAt)
	rows, err := queryer.QueryContext(ctx, `SELECT pack_id, vault_id, device_id, device_seq, event_id, event_hash, state, reason_code, recorded_at FROM sync_replay_items WHERE pack_id = ? ORDER BY device_seq`, packID)
	if err != nil {
		return syncstate.ReplayResult{}, false, err
	}
	defer rows.Close()
	items := make([]syncstate.ReplayItem, 0, batch.EventCount)
	for rows.Next() {
		var item syncstate.ReplayItem
		var deviceSeq int64
		var state, recordedAt string
		if err := rows.Scan(&item.PackID, &item.VaultID, &item.DeviceID, &deviceSeq, &item.EventID, &item.EventHash, &state, &item.ReasonCode, &recordedAt); err != nil {
			return syncstate.ReplayResult{}, false, err
		}
		item.DeviceSeq = uint64(deviceSeq)
		item.State = syncstate.ReplayState(state)
		item.RecordedAt, _ = time.Parse(time.RFC3339Nano, recordedAt)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return syncstate.ReplayResult{}, false, err
	}
	result := syncstate.ReplayResult{Batch: batch, Items: items}
	if result.Validate() != nil {
		return syncstate.ReplayResult{}, false, syncstore.ErrReplayConflict
	}
	return result, true, nil
}

func (s *Store) replayOneEvent(ctx context.Context, tx *sql.Tx, input syncstore.ApplyReplayInput, candidate syncstore.ReplayEvent) (syncstate.ReplayState, string, error) {
	existing, err := s.getEventInTx(tx, candidate.Record.ID)
	if err == nil {
		originDevice, originSequence, originKind, found, originErr := getSyncEventOrigin(ctx, tx, candidate.Record.ID)
		if originErr != nil {
			return "", "", originErr
		}
		if sameReplayEvent(existing, candidate.Record) && found && originDevice == input.DeviceID && originSequence == candidate.DeviceSeq && originKind == "imported" {
			return syncstate.ReplayApplied, "already_applied", nil
		}
		return syncstate.ReplayConflicted, "event_id_conflict", nil
	}
	if !errors.Is(err, ErrNotFound) {
		return "", "", err
	}
	if err := s.insertEvent(ctx, tx, candidate.Record); err != nil {
		return "", "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_event_origins(event_id, vault_id, device_id, device_seq, origin_kind) VALUES(?, ?, ?, ?, 'imported')`, candidate.Record.ID, input.VaultID, input.DeviceID, candidate.DeviceSeq); err != nil {
		return "", "", fmt.Errorf("insert imported event origin: %w", err)
	}
	return s.materializeReplayEvent(ctx, tx, input.DeviceID, candidate.Record)
}

func (s *Store) materializeReplayEvent(ctx context.Context, tx *sql.Tx, sourceDeviceID string, record event.Record) (syncstate.ReplayState, string, error) {
	expectedSchema, known := syncableEventSchemas[record.Type]
	if !known {
		return syncstate.ReplayQuarantined, "event_type_not_syncable", nil
	}
	if record.SchemaVersion != expectedSchema {
		return syncstate.ReplayQuarantined, "schema_unsupported", nil
	}
	switch record.Type {
	case taskContractCreatedEventType, taskContractRevisedEventType, taskContractSnapshotEventType:
		return s.materializeTaskReplay(ctx, tx, record)
	case decisionCreatedEventType, decisionAnsweredEventType, decisionSupersededEventType, decisionResolvedEventType:
		return s.materializeDecisionReplay(ctx, tx, record)
	case memoryCandidateCreatedEventType, memoryStateChangedEventType, memorySnapshotEventType:
		return s.materializeMemoryReplay(ctx, tx, record)
	case syncDeviceRevocationEventType:
		return s.materializeSyncDeviceRevocation(ctx, tx, sourceDeviceID, record)
	default:
		return syncstate.ReplayQuarantined, "event_type_not_syncable", nil
	}
}

func (s *Store) materializeSyncDeviceRevocation(ctx context.Context, tx *sql.Tx, sourceDeviceID string, record event.Record) (syncstate.ReplayState, string, error) {
	var payload syncDeviceRevocationPayload
	if decodeReplayPayload(record.Payload, &payload) != nil || record.Sensitivity != event.SensitivityPrivate || payload.AuthorityDeviceID != sourceDeviceID || payload.TargetDeviceID == "" || len(payload.TargetDeviceID) > syncstate.MaxDeviceIDLength || payload.ExpectedTargetRevision < 1 {
		return syncstate.ReplayQuarantined, "payload_invalid", nil
	}
	publicKey, err := base64.RawStdEncoding.DecodeString(payload.TargetPublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return syncstate.ReplayQuarantined, "payload_invalid", nil
	}
	revokedAt, err := time.Parse(time.RFC3339Nano, payload.RevokedAt)
	if err != nil || !revokedAt.Equal(record.OccurredAt) {
		return syncstate.ReplayQuarantined, "payload_invalid", nil
	}
	authority, exists, err := getSyncDevicePointer(ctx, tx, record.VaultID, sourceDeviceID)
	if err != nil {
		return "", "", err
	}
	if !exists || authority.State != syncstate.DeviceActive {
		return syncstate.ReplayQuarantined, "authority_revoked", nil
	}
	target, exists, err := getSyncDevicePointer(ctx, tx, record.VaultID, payload.TargetDeviceID)
	if err != nil {
		return "", "", err
	}
	if !exists {
		if payload.ExpectedTargetRevision != 1 {
			return syncstate.ReplayQuarantined, "dependency_missing", nil
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO sync_devices(vault_id, device_id, public_key, state, revision, next_sequence, created_at, updated_at, last_event_id) VALUES(?, ?, ?, 'revoked', 2, 1, ?, ?, ?)`, record.VaultID, payload.TargetDeviceID, publicKey, payload.RevokedAt, payload.RevokedAt, record.ID); err != nil {
			return "", "", err
		}
		return syncstate.ReplayApplied, "revocation_tombstone_created", nil
	}
	if !bytes.Equal(target.PublicKey, publicKey) {
		return syncstate.ReplayConflicted, "device_key_conflict", nil
	}
	if target.State == syncstate.DeviceRevoked {
		return syncstate.ReplayApplied, "equivalent_revocation", nil
	}
	if target.Revision != payload.ExpectedTargetRevision || revokedAt.Before(target.UpdatedAt) {
		return syncstate.ReplayConflicted, "aggregate_revision_conflict", nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE sync_devices SET state = 'revoked', revision = ?, updated_at = ?, last_event_id = ? WHERE vault_id = ? AND device_id = ? AND state = 'active' AND revision = ?`, target.Revision+1, payload.RevokedAt, record.ID, record.VaultID, target.DeviceID, target.Revision)
	if err != nil {
		return "", "", err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return "", "", syncstore.ErrReplayConflict
	}
	return syncstate.ReplayApplied, "device_revoked", nil
}

func (s *Store) materializeTaskReplay(ctx context.Context, tx *sql.Tx, record event.Record) (syncstate.ReplayState, string, error) {
	var payload taskContractPayload
	if decodeReplayPayload(record.Payload, &payload) != nil {
		return syncstate.ReplayQuarantined, "payload_invalid", nil
	}
	payloadTime, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
	workspaceID, sourceWorkspaceHash, workspaceErr := taskPayloadWorkspace(record.VaultID, record.SchemaVersion, payload)
	contract, contractErr := taskContractFromPayload(payload, record.ID)
	if err != nil || !payloadTime.Equal(record.OccurredAt) || workspaceErr != nil || contractErr != nil || record.Sensitivity != event.SensitivityPrivate {
		return syncstate.ReplayQuarantined, "payload_invalid", nil
	}
	isCreation := record.Type == taskContractCreatedEventType || record.Type == taskContractSnapshotEventType && payload.Revision == 1
	if isCreation {
		if payload.Revision != 1 {
			return syncstate.ReplayQuarantined, "payload_invalid", nil
		}
		if _, err := scanTaskPointer(tx.QueryRowContext(ctx, taskSelect+" WHERE task_id = ?", payload.TaskID)); err == nil {
			return syncstate.ReplayConflicted, "aggregate_exists", nil
		} else if !errors.Is(err, taskstore.ErrNotFound) {
			return "", "", err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO tasks(task_id, vault_id, workspace_root_hash, baseline_commit, status, current_revision, created_at, updated_at, last_event_id, workspace_id) VALUES(?, ?, ?, ?, ?, 1, ?, ?, ?, ?)`, payload.TaskID, record.VaultID, sourceWorkspaceHash, payload.BaselineCommit, string(task.StatusContracted), payload.CreatedAt, payload.CreatedAt, record.ID, workspaceID); err != nil {
			return "", "", err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_contract_revisions(task_id, revision, baseline_commit, created_at, event_id) VALUES(?, 1, ?, ?, ?)`, payload.TaskID, payload.BaselineCommit, payload.CreatedAt, record.ID); err != nil {
			return "", "", err
		}
		return syncstate.ReplayApplied, "applied", nil
	}
	pointer, err := scanTaskPointer(tx.QueryRowContext(ctx, taskSelect+" WHERE task_id = ? AND vault_id = ?", payload.TaskID, record.VaultID))
	if errors.Is(err, taskstore.ErrNotFound) {
		return syncstate.ReplayQuarantined, "dependency_missing", nil
	}
	if err != nil {
		return "", "", err
	}
	currentEvent, err := s.getEventInTx(tx, pointer.lastEventID)
	if err != nil {
		return "", "", err
	}
	current, err := taskFromEvent(currentEvent, pointer)
	if err != nil {
		return "", "", err
	}
	if current.Status != task.StatusContracted || payload.Revision != pointer.currentRevision+1 || payload.BaselineCommit != current.BaselineCommit || workspaceID != current.WorkspaceID || sourceWorkspaceHash != pointer.workspaceRootHash || payloadTime.Before(current.UpdatedAt) {
		return syncstate.ReplayConflicted, "aggregate_revision_conflict", nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_contract_revisions(task_id, revision, baseline_commit, created_at, event_id) VALUES(?, ?, ?, ?, ?)`, payload.TaskID, payload.Revision, payload.BaselineCommit, payload.CreatedAt, record.ID); err != nil {
		return "", "", err
	}
	result, err := tx.ExecContext(ctx, `UPDATE tasks SET current_revision = ?, updated_at = ?, last_event_id = ? WHERE task_id = ? AND vault_id = ? AND current_revision = ?`, payload.Revision, payload.CreatedAt, record.ID, payload.TaskID, record.VaultID, pointer.currentRevision)
	if err != nil {
		return "", "", err
	}
	if rows, _ := result.RowsAffected(); rows != 1 || contract.Revision != payload.Revision {
		return "", "", syncstore.ErrReplayConflict
	}
	return syncstate.ReplayApplied, "applied", nil
}

func (s *Store) materializeDecisionReplay(ctx context.Context, tx *sql.Tx, record event.Record) (syncstate.ReplayState, string, error) {
	switch record.Type {
	case decisionCreatedEventType, decisionSupersededEventType:
		var payload decisionQuestionPayload
		if decodeReplayPayload(record.Payload, &payload) != nil || payload.TaskID == "" || payload.ExpectedRepositoryRevision == "" || payload.Category == "" || record.Sensitivity != event.SensitivityPrivate {
			return syncstate.ReplayQuarantined, "payload_invalid", nil
		}
		createdAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
		if err != nil || !createdAt.Equal(record.OccurredAt) {
			return syncstate.ReplayQuarantined, "payload_invalid", nil
		}
		if record.Type == decisionCreatedEventType {
			if payload.QuestionRevision != 1 {
				return syncstate.ReplayQuarantined, "payload_invalid", nil
			}
			if _, err := scanDecisionPointer(tx.QueryRowContext(ctx, decisionSelect+" WHERE decision_id = ?", payload.DecisionID)); err == nil {
				return syncstate.ReplayConflicted, "aggregate_exists", nil
			} else if !errors.Is(err, decisionstore.ErrNotFound) {
				return "", "", err
			}
			if err := requireTaskInVault(ctx, tx, payload.TaskID, record.VaultID, payload.ExpectedRepositoryRevision); err != nil {
				return syncstate.ReplayQuarantined, "dependency_missing", nil
			}
			if _, err := decisionResultFromQuestionPayload(record.VaultID, payload.TaskID, payload.Category, payload.ExpectedRepositoryRevision, payload, record.ID); err != nil {
				return syncstate.ReplayQuarantined, "payload_invalid", nil
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO decisions(decision_id, vault_id, task_id, question_revision, category, state, expected_repository_revision, created_at, updated_at, question_event_id, last_event_id) VALUES(?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?)`, payload.DecisionID, record.VaultID, payload.TaskID, string(payload.Category), string(decision.StateOpen), payload.ExpectedRepositoryRevision, payload.CreatedAt, payload.CreatedAt, record.ID, record.ID); err != nil {
				return "", "", err
			}
			return syncstate.ReplayApplied, "applied", nil
		}
		pointer, err := scanDecisionPointer(tx.QueryRowContext(ctx, decisionSelect+" WHERE decision_id = ? AND vault_id = ?", payload.DecisionID, record.VaultID))
		if errors.Is(err, decisionstore.ErrNotFound) {
			return syncstate.ReplayQuarantined, "dependency_missing", nil
		}
		if err != nil {
			return "", "", err
		}
		if payload.TaskID != pointer.taskID || string(payload.Category) != pointer.category || payload.ExpectedRepositoryRevision != pointer.repositoryRevision || payload.QuestionRevision != pointer.questionRevision+1 {
			return syncstate.ReplayConflicted, "aggregate_revision_conflict", nil
		}
		if _, err := decisionResultFromQuestionPayload(record.VaultID, pointer.taskID, payload.Category, pointer.repositoryRevision, payload, record.ID); err != nil {
			return syncstate.ReplayQuarantined, "payload_invalid", nil
		}
		result, err := tx.ExecContext(ctx, `UPDATE decisions SET question_revision = ?, state = ?, updated_at = ?, question_event_id = ?, last_event_id = ? WHERE decision_id = ? AND vault_id = ? AND question_revision = ?`, payload.QuestionRevision, string(decision.StateOpen), payload.CreatedAt, record.ID, record.ID, payload.DecisionID, record.VaultID, pointer.questionRevision)
		if err != nil {
			return "", "", err
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return "", "", syncstore.ErrReplayConflict
		}
		return syncstate.ReplayApplied, "applied", nil

	case decisionAnsweredEventType:
		var payload decisionAnswerPayload
		if decodeReplayPayload(record.Payload, &payload) != nil || record.Sensitivity != event.SensitivityPrivate {
			return syncstate.ReplayQuarantined, "payload_invalid", nil
		}
		answer, _, err := answerFromEvent(record)
		if err != nil || !answer.CreatedAt.Equal(record.OccurredAt) || payload.QuestionEventID == "" {
			return syncstate.ReplayQuarantined, "payload_invalid", nil
		}
		pointer, err := scanDecisionPointer(tx.QueryRowContext(ctx, decisionSelect+" WHERE decision_id = ? AND vault_id = ?", payload.DecisionID, record.VaultID))
		if errors.Is(err, decisionstore.ErrNotFound) {
			return syncstate.ReplayQuarantined, "dependency_missing", nil
		}
		if err != nil {
			return "", "", err
		}
		if pointer.questionRevision != payload.QuestionRevision || pointer.repositoryRevision != payload.ExpectedRepositoryRevision || pointer.questionEventID != payload.QuestionEventID {
			return syncstate.ReplayConflicted, "aggregate_revision_conflict", nil
		}
		questionEvent, err := s.getEventInTx(tx, pointer.questionEventID)
		if err != nil {
			return "", "", err
		}
		questionResult, err := decisionResultFromPointer(questionEvent, pointer)
		if err != nil || answer.SelectedOptionID != "" && !questionHasOption(questionResult.Question, answer.SelectedOptionID) {
			return syncstate.ReplayQuarantined, "payload_invalid", nil
		}
		hashBytes, err := requestHash(struct{ SelectedOptionID, Text string }{answer.SelectedOptionID, strings.TrimSpace(answer.Text)})
		if err != nil {
			return "", "", err
		}
		answerHash := hex.EncodeToString(hashBytes)
		var equivalent string
		if err := tx.QueryRowContext(ctx, `SELECT event_id FROM decision_answers WHERE decision_id = ? AND question_revision = ? AND answer_hash = ?`, answer.DecisionID, answer.QuestionRevision, answerHash).Scan(&equivalent); err == nil {
			return syncstate.ReplayApplied, "equivalent_answer", nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return "", "", err
		}
		var answerIDExists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM decision_answers WHERE answer_id = ?`, answer.ID).Scan(&answerIDExists); err != nil {
			return "", "", err
		}
		if answerIDExists != 0 {
			return syncstate.ReplayConflicted, "aggregate_exists", nil
		}
		var answerCount int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM decision_answers WHERE decision_id = ? AND question_revision = ?`, answer.DecisionID, answer.QuestionRevision).Scan(&answerCount); err != nil {
			return "", "", err
		}
		resultingState := decision.StateAnswered
		if answerCount > 0 {
			resultingState = decision.StateConflicted
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO decision_answers(answer_id, decision_id, question_revision, expected_repository_revision, answer_hash, created_at, event_id) VALUES(?, ?, ?, ?, ?, ?, ?)`, answer.ID, answer.DecisionID, answer.QuestionRevision, answer.ExpectedRepositoryRevision, answerHash, payload.CreatedAt, record.ID); err != nil {
			return "", "", err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE decisions SET state = ?, updated_at = ?, last_event_id = ? WHERE decision_id = ?`, string(resultingState), payload.CreatedAt, record.ID, answer.DecisionID); err != nil {
			return "", "", err
		}
		return syncstate.ReplayApplied, "applied", nil

	case decisionResolvedEventType:
		var payload decisionResolutionPayload
		if decodeReplayPayload(record.Payload, &payload) != nil || record.Sensitivity != event.SensitivityPrivate {
			return syncstate.ReplayQuarantined, "payload_invalid", nil
		}
		resolvedAt, err := time.Parse(time.RFC3339Nano, payload.ResolvedAt)
		if err != nil || !resolvedAt.Equal(record.OccurredAt) {
			return syncstate.ReplayQuarantined, "payload_invalid", nil
		}
		pointer, err := scanDecisionPointer(tx.QueryRowContext(ctx, decisionSelect+" WHERE decision_id = ? AND vault_id = ?", payload.DecisionID, record.VaultID))
		if errors.Is(err, decisionstore.ErrNotFound) {
			return syncstate.ReplayQuarantined, "dependency_missing", nil
		}
		if err != nil {
			return "", "", err
		}
		if pointer.questionRevision != payload.QuestionRevision || pointer.repositoryRevision != payload.ExpectedRepositoryRevision || pointer.questionEventID != payload.QuestionEventID || decision.State(pointer.state) != decision.StateConflicted {
			return syncstate.ReplayConflicted, "aggregate_revision_conflict", nil
		}
		var selected int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM decision_answers WHERE answer_id = ? AND decision_id = ? AND question_revision = ?`, payload.SelectedAnswerID, payload.DecisionID, payload.QuestionRevision).Scan(&selected); err != nil {
			return "", "", err
		}
		if selected != 1 {
			return syncstate.ReplayQuarantined, "dependency_missing", nil
		}
		result, err := tx.ExecContext(ctx, `UPDATE decisions SET state = ?, updated_at = ?, last_event_id = ? WHERE decision_id = ? AND question_revision = ? AND state = ?`, string(decision.StateAnswered), payload.ResolvedAt, record.ID, payload.DecisionID, payload.QuestionRevision, string(decision.StateConflicted))
		if err != nil {
			return "", "", err
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return "", "", syncstore.ErrReplayConflict
		}
		return syncstate.ReplayApplied, "applied", nil
	}
	return syncstate.ReplayQuarantined, "event_type_not_syncable", nil
}

func (s *Store) materializeMemoryReplay(ctx context.Context, tx *sql.Tx, record event.Record) (syncstate.ReplayState, string, error) {
	var payload memoryPayload
	if decodeReplayPayload(record.Payload, &payload) != nil {
		return syncstate.ReplayQuarantined, "payload_invalid", nil
	}
	incoming, err := memoryFromEvent(record)
	if err != nil || !incoming.UpdatedAt.Equal(record.OccurredAt) {
		return syncstate.ReplayQuarantined, "payload_invalid", nil
	}
	if record.Type == memoryCandidateCreatedEventType || record.Type == memorySnapshotEventType && incoming.Revision == 1 {
		if incoming.State != memory.StateCandidate || incoming.Revision != 1 || incoming.CreatedEventID != record.ID || !incoming.CreatedAt.Equal(incoming.UpdatedAt) {
			return syncstate.ReplayQuarantined, "payload_invalid", nil
		}
		if _, err := scanMemoryPointer(tx.QueryRowContext(ctx, memorySelect+" WHERE memory_id = ?", incoming.ID)); err == nil {
			return syncstate.ReplayConflicted, "aggregate_exists", nil
		} else if !errors.Is(err, memorystore.ErrNotFound) {
			return "", "", err
		}
		if err := requireMemoryEvidence(ctx, tx, record.VaultID, incoming.EvidenceEventIDs); err != nil {
			if errors.Is(err, memorystore.ErrEvidenceNotFound) {
				return syncstate.ReplayQuarantined, "dependency_missing", nil
			}
			return "", "", err
		}
		workspaceHash, workspaceID, err := memoryScopeIdentity(record.VaultID, incoming.Scope)
		if err != nil {
			return syncstate.ReplayQuarantined, "payload_invalid", nil
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO memory_records(memory_id, vault_id, kind, state, scope_kind, workspace_root_hash, workspace_id, sensitivity, confidence, revision, created_at, updated_at, reviewed_at, expires_at, superseded_by_memory_id, created_event_id, last_event_id) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, incoming.ID, record.VaultID, string(incoming.Kind), string(incoming.State), string(incoming.Scope.Kind), workspaceHash, workspaceID, string(incoming.Sensitivity), incoming.Confidence, incoming.Revision, payload.CreatedAt, payload.UpdatedAt, payload.ReviewedAt, payload.ExpiresAt, payload.SupersededBy, incoming.CreatedEventID, record.ID); err != nil {
			return "", "", err
		}
		return syncstate.ReplayApplied, "applied", nil
	}
	pointer, err := scanMemoryPointer(tx.QueryRowContext(ctx, memorySelect+" WHERE memory_id = ? AND vault_id = ?", incoming.ID, record.VaultID))
	if errors.Is(err, memorystore.ErrNotFound) {
		return syncstate.ReplayQuarantined, "dependency_missing", nil
	}
	if err != nil {
		return "", "", err
	}
	currentEvent, err := s.getEventInTx(tx, pointer.lastEventID)
	if err != nil {
		return "", "", err
	}
	current, err := memoryFromEvent(currentEvent)
	if err != nil {
		return "", "", err
	}
	current, err = normalizeMemoryScope(current, pointer)
	if err != nil {
		return "", "", err
	}
	if incoming.Revision != current.Revision+1 || !memory.CanTransition(current.State, incoming.State) || !memoryReplayIdentityEqual(current, incoming) || incoming.UpdatedAt.Before(current.UpdatedAt) {
		return syncstate.ReplayConflicted, "aggregate_revision_conflict", nil
	}
	if current.ReviewedAt.IsZero() {
		if incoming.ReviewedAt.IsZero() || !incoming.ReviewedAt.Equal(incoming.UpdatedAt) {
			return syncstate.ReplayQuarantined, "payload_invalid", nil
		}
	} else if !incoming.ReviewedAt.Equal(current.ReviewedAt) {
		return syncstate.ReplayConflicted, "aggregate_revision_conflict", nil
	}
	if incoming.State == memory.StateSuperseded {
		replacement, err := scanMemoryPointer(tx.QueryRowContext(ctx, memorySelect+" WHERE memory_id = ? AND vault_id = ?", incoming.SupersededBy, record.VaultID))
		if errors.Is(err, memorystore.ErrNotFound) {
			return syncstate.ReplayQuarantined, "dependency_missing", nil
		}
		if err != nil {
			return "", "", err
		}
		if replacement.state != string(memory.StateApproved) && replacement.state != string(memory.StateStable) || replacement.scopeKind != pointer.scopeKind || replacement.workspaceID != pointer.workspaceID || replacement.workspaceRootHash != pointer.workspaceRootHash {
			return syncstate.ReplayConflicted, "replacement_invalid", nil
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE memory_records SET state = ?, revision = ?, updated_at = ?, reviewed_at = ?, expires_at = ?, superseded_by_memory_id = ?, last_event_id = ? WHERE memory_id = ? AND vault_id = ? AND revision = ?`, string(incoming.State), incoming.Revision, payload.UpdatedAt, payload.ReviewedAt, payload.ExpiresAt, payload.SupersededBy, record.ID, incoming.ID, record.VaultID, current.Revision)
	if err != nil {
		return "", "", err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return "", "", syncstore.ErrReplayConflict
	}
	return syncstate.ReplayApplied, "applied", nil
}

func memoryReplayIdentityEqual(left, right memory.Record) bool {
	return left.ID == right.ID && left.VaultID == right.VaultID && left.Kind == right.Kind && left.Scope == right.Scope && left.Statement == right.Statement && left.Rationale == right.Rationale && slices.Equal(left.Applicability.GoalTerms, right.Applicability.GoalTerms) && slices.Equal(left.EvidenceEventIDs, right.EvidenceEventIDs) && left.SourceActor == right.SourceActor && left.Confidence == right.Confidence && left.Sensitivity == right.Sensitivity && left.CreatedAt.Equal(right.CreatedAt) && left.CreatedEventID == right.CreatedEventID
}

func syncableEventFilter(alias string) (string, []any) {
	types := make([]string, 0, len(syncableEventSchemas))
	for eventType := range syncableEventSchemas {
		types = append(types, eventType)
	}
	slices.Sort(types)
	conditions := make([]string, 0, len(types))
	arguments := make([]any, 0, len(types)*2)
	for _, eventType := range types {
		conditions = append(conditions, fmt.Sprintf("(%s.event_type = ? AND %s.schema_version = ?)", alias, alias))
		arguments = append(arguments, eventType, syncableEventSchemas[eventType])
	}
	return "(" + strings.Join(conditions, " OR ") + ")", arguments
}

func getSyncEventOrigin(ctx context.Context, queryer syncQueryer, eventID string) (string, uint64, string, bool, error) {
	var deviceID, kind string
	var sequence int64
	err := queryer.QueryRowContext(ctx, `SELECT device_id, device_seq, origin_kind FROM sync_event_origins WHERE event_id = ?`, eventID).Scan(&deviceID, &sequence, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, "", false, nil
	}
	if err != nil {
		return "", 0, "", false, err
	}
	return deviceID, uint64(sequence), kind, true, nil
}

func sameReplayEvent(left, right event.Record) bool {
	return left.ID == right.ID && left.VaultID == right.VaultID && left.Type == right.Type && left.SchemaVersion == right.SchemaVersion && left.Sensitivity == right.Sensitivity && left.OccurredAt.Equal(right.OccurredAt) && bytes.Equal(left.Payload, right.Payload)
}

func sameReplayInput(existing syncstate.ReplayResult, input syncstore.ApplyReplayInput) bool {
	if existing.Batch.PackID != input.PackID || existing.Batch.VaultID != input.VaultID || existing.Batch.DeviceID != input.DeviceID || len(existing.Items) != len(input.Events) {
		return false
	}
	for index, candidate := range input.Events {
		item := existing.Items[index]
		if item.DeviceSeq != candidate.DeviceSeq || item.EventID != candidate.Record.ID || item.EventHash != replayEventHash(candidate.Record) {
			return false
		}
	}
	return true
}

func replayEventHash(record event.Record) string {
	encoded, _ := json.Marshal(struct {
		ID            string            `json:"event_id"`
		VaultID       string            `json:"vault_id"`
		Type          string            `json:"type"`
		SchemaVersion int               `json:"schema_version"`
		Sensitivity   event.Sensitivity `json:"sensitivity"`
		Payload       []byte            `json:"payload"`
		OccurredAt    string            `json:"occurred_at"`
	}{record.ID, record.VaultID, record.Type, record.SchemaVersion, record.Sensitivity, record.Payload, record.OccurredAt.UTC().Format(time.RFC3339Nano)})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func decodeReplayPayload(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing replay payload")
		}
		return err
	}
	return nil
}
