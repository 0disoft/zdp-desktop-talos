package sqliteevent

import (
	"context"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
)

func (s *Store) ListTaskContracts(ctx context.Context, input taskstore.ListInput) ([]taskstore.Created, error) {
	root, hash, err := normalizedWorkspaceRoot(input.WorkspaceRoot)
	if err != nil || input.VaultID == "" || input.BaselineCommit == "" || input.Limit < 1 || input.Limit > 50 {
		return nil, taskstore.ErrInvalidCommand
	}
	result := make([]taskstore.Created, 0)
	pointer, found, err := getWorkspaceMappingByLocalHash(ctx, s.db, input.VaultID, hash)
	if err != nil || !found {
		return result, err
	}
	mapping, err := s.workspaceMappingFromPointer(ctx, s.db, pointer)
	if err != nil {
		return nil, err
	}
	if mapping.LocalRoot != root {
		return nil, taskstore.ErrInvalidCommand
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.event_id, e.vault_id, e.event_type, e.schema_version, e.sensitivity, e.payload_envelope, e.occurred_at,
		t.task_id, t.vault_id, t.workspace_root_hash, t.baseline_commit, COALESCE(o.status,t.status), t.current_revision, t.created_at, t.updated_at, t.last_event_id, t.workspace_id, c.created_at
		FROM tasks t JOIN task_contract_revisions c ON c.task_id=t.task_id AND c.revision=t.current_revision
		JOIN events e ON e.event_id=t.last_event_id AND e.event_id=c.event_id AND e.vault_id=t.vault_id
		LEFT JOIN task_outcomes o ON o.task_id=t.task_id
		WHERE t.vault_id=? AND t.workspace_id=? AND t.baseline_commit=?
		ORDER BY t.updated_at DESC, t.task_id LIMIT ?`, input.VaultID, mapping.WorkspaceID, input.BaselineCommit, input.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p taskPointer
		var contractAt string
		e, err := s.scanRecordWithTail(rows, &p.taskID, &p.vaultID, &p.workspaceRootHash, &p.baselineCommit, &p.status, &p.currentRevision, &p.createdAt, &p.updatedAt, &p.lastEventID, &p.workspaceID, &contractAt)
		if err != nil {
			return nil, err
		}
		record, err := taskFromEvent(e, p)
		if err != nil {
			return nil, err
		}
		record.WorkspaceRoot = mapping.LocalRoot
		if err := record.Validate(); err != nil {
			return nil, err
		}
		contract, err := contractFromEvent(e, record.ID, record.CurrentRevision, record.BaselineCommit, contractAt)
		if err != nil {
			return nil, err
		}
		result = append(result, taskstore.Created{Task: record, Contract: contract})
	}
	return result, rows.Err()
}
