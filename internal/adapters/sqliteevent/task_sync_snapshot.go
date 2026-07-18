package sqliteevent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type taskSyncSnapshotCandidate struct {
	taskID, vaultID, workspaceID, sourceWorkspaceHash, sourceEventID string
	revision                                                         int
}

func (s *Store) ReconcileTaskSyncSnapshots(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT r.task_id, t.vault_id, t.workspace_id, t.workspace_root_hash, r.revision, r.event_id
		FROM task_contract_revisions r
		JOIN tasks t ON t.task_id = r.task_id
		JOIN events e ON e.event_id = r.event_id
		LEFT JOIN sync_event_origins o ON o.event_id = r.event_id
		LEFT JOIN task_sync_snapshots b ON b.task_id = r.task_id AND b.revision = r.revision
		WHERE e.schema_version = ? AND COALESCE(o.origin_kind, 'local') = 'local' AND b.task_id IS NULL
		ORDER BY t.created_at, r.task_id, r.revision`, taskContractLegacyEventSchemaVersion)
	if err != nil {
		return fmt.Errorf("list legacy task sync snapshots: %w", err)
	}
	var candidates []taskSyncSnapshotCandidate
	for rows.Next() {
		var candidate taskSyncSnapshotCandidate
		if err := rows.Scan(&candidate.taskID, &candidate.vaultID, &candidate.workspaceID, &candidate.sourceWorkspaceHash, &candidate.revision, &candidate.sourceEventID); err != nil {
			_ = rows.Close()
			return err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, candidate := range candidates {
		if err := s.createTaskSyncSnapshot(ctx, candidate); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) createTaskSyncSnapshot(ctx context.Context, candidate taskSyncSnapshotCandidate) error {
	source, err := s.Get(ctx, candidate.sourceEventID)
	if err != nil {
		return err
	}
	if source.SchemaVersion != taskContractLegacyEventSchemaVersion || source.VaultID != candidate.vaultID {
		return fmt.Errorf("legacy task sync source is incompatible")
	}
	var payload taskContractPayload
	if err := json.Unmarshal(source.Payload, &payload); err != nil {
		return fmt.Errorf("decode legacy task sync source: %w", err)
	}
	workspaceID, sourceHash, err := taskPayloadWorkspace(candidate.vaultID, source.SchemaVersion, payload)
	if err != nil || workspaceID != candidate.workspaceID || sourceHash != candidate.sourceWorkspaceHash || payload.TaskID != candidate.taskID || payload.Revision != candidate.revision {
		return fmt.Errorf("legacy task sync source identity mismatch")
	}
	payload.WorkspaceID = workspaceID
	payload.SourceWorkspaceHash = sourceHash
	payload.WorkspaceRoot = ""
	occurredAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
	if err != nil || !occurredAt.Equal(source.OccurredAt) {
		return fmt.Errorf("legacy task sync source time mismatch")
	}
	snapshot, err := s.taskEventOfType(candidate.vaultID, taskContractSnapshotEventType, payload, occurredAt)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT snapshot_event_id FROM task_sync_snapshots WHERE task_id = ? AND revision = ?`, candidate.taskID, candidate.revision).Scan(&existing)
	if err == nil {
		return nil
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err := s.insertEvent(ctx, tx, snapshot); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_sync_snapshots(task_id, revision, source_event_id, snapshot_event_id, created_at) VALUES(?, ?, ?, ?, ?)`, candidate.taskID, candidate.revision, candidate.sourceEventID, snapshot.ID, s.now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}
