package sqliteevent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspacemapping"
)

type memorySyncSnapshotCandidate struct {
	memoryID, sourceEventID string
	revision                int
}

func (s *Store) ReconcileMemoryWorkspaceIDs(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT memory_id, vault_id, scope_kind, workspace_root_hash, workspace_id FROM memory_records WHERE workspace_id = '' ORDER BY created_at, memory_id`)
	if err != nil {
		return fmt.Errorf("list memory workspace identities: %w", err)
	}
	type candidate struct{ memoryID, workspaceID string }
	var updates []candidate
	for rows.Next() {
		var memoryID, vaultID, scopeKind, sourceHash, workspaceID string
		if err := rows.Scan(&memoryID, &vaultID, &scopeKind, &sourceHash, &workspaceID); err != nil {
			_ = rows.Close()
			return err
		}
		switch memory.ScopeKind(scopeKind) {
		case memory.ScopeVault:
			if sourceHash != "" || workspaceID != "" {
				_ = rows.Close()
				return fmt.Errorf("Vault-scoped memory has workspace identity")
			}
		case memory.ScopeWorkspace:
			expected := workspacemapping.ID(vaultID, sourceHash)
			portable := memory.Scope{Kind: memory.ScopeWorkspace, WorkspaceID: expected, SourceWorkspaceHash: sourceHash}
			if portable.Validate() != nil || workspaceID != "" && workspaceID != expected {
				_ = rows.Close()
				return fmt.Errorf("workspace-scoped memory identity is invalid")
			}
			if workspaceID == "" {
				updates = append(updates, candidate{memoryID: memoryID, workspaceID: expected})
			}
		default:
			_ = rows.Close()
			return fmt.Errorf("memory scope kind is invalid")
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(updates) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, update := range updates {
		result, err := tx.ExecContext(ctx, `UPDATE memory_records SET workspace_id = ? WHERE memory_id = ? AND workspace_id = ''`, update.workspaceID, update.memoryID)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return fmt.Errorf("memory workspace identity update raced")
		}
	}
	return tx.Commit()
}

func (s *Store) ReconcileMemorySyncSnapshots(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT e.event_id
		FROM events e
		LEFT JOIN sync_event_origins o ON o.event_id = e.event_id
		LEFT JOIN memory_sync_snapshots b ON b.source_event_id = e.event_id
		WHERE e.schema_version = ? AND e.event_type IN (?, ?) AND COALESCE(o.origin_kind, 'local') = 'local' AND b.source_event_id IS NULL
		ORDER BY e.occurred_at, e.event_id`, memoryLegacyEventSchemaVersion, memoryCandidateCreatedEventType, memoryStateChangedEventType)
	if err != nil {
		return fmt.Errorf("list legacy memory sync snapshots: %w", err)
	}
	var sourceEventIDs []string
	for rows.Next() {
		var sourceEventID string
		if err := rows.Scan(&sourceEventID); err != nil {
			_ = rows.Close()
			return err
		}
		sourceEventIDs = append(sourceEventIDs, sourceEventID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	var candidates []memorySyncSnapshotCandidate
	for _, sourceEventID := range sourceEventIDs {
		source, err := s.Get(ctx, sourceEventID)
		if err != nil {
			return err
		}
		record, err := memoryFromEvent(source)
		if err != nil {
			return fmt.Errorf("decode legacy memory sync source: %w", err)
		}
		candidates = append(candidates, memorySyncSnapshotCandidate{memoryID: record.ID, revision: record.Revision, sourceEventID: sourceEventID})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].memoryID == candidates[j].memoryID {
			return candidates[i].revision < candidates[j].revision
		}
		return candidates[i].memoryID < candidates[j].memoryID
	})
	for _, candidate := range candidates {
		if err := s.createMemorySyncSnapshot(ctx, candidate); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) createMemorySyncSnapshot(ctx context.Context, candidate memorySyncSnapshotCandidate) error {
	source, err := s.Get(ctx, candidate.sourceEventID)
	if err != nil {
		return err
	}
	if source.SchemaVersion != memoryLegacyEventSchemaVersion {
		return fmt.Errorf("legacy memory sync source is incompatible")
	}
	var payload memoryPayload
	if err := json.Unmarshal(source.Payload, &payload); err != nil {
		return fmt.Errorf("decode legacy memory sync source: %w", err)
	}
	legacy, err := memoryFromEvent(source)
	if err != nil || legacy.ID != candidate.memoryID || legacy.Revision != candidate.revision {
		return fmt.Errorf("legacy memory sync source identity mismatch")
	}
	pointer, err := scanMemoryPointer(s.db.QueryRowContext(ctx, memorySelect+" WHERE memory_id = ? AND vault_id = ?", candidate.memoryID, source.VaultID))
	if err != nil {
		return err
	}
	sourceHash, workspaceID, err := memoryScopeIdentity(source.VaultID, payload.Scope)
	if err != nil || sourceHash != pointer.workspaceRootHash || workspaceID != pointer.workspaceID {
		return fmt.Errorf("legacy memory sync scope mismatch")
	}
	if payload.Scope.Kind == memory.ScopeWorkspace {
		payload.Scope = memory.Scope{Kind: memory.ScopeWorkspace, WorkspaceID: workspaceID, SourceWorkspaceHash: sourceHash}
	}
	if payload.Revision == 1 {
		if source.Type != memoryCandidateCreatedEventType || payload.State != memory.StateCandidate {
			return fmt.Errorf("legacy memory candidate snapshot is invalid")
		}
		payload.CreatedEventID = ""
	} else {
		if source.Type != memoryStateChangedEventType {
			return fmt.Errorf("legacy memory transition snapshot is invalid")
		}
		if err := s.db.QueryRowContext(ctx, `SELECT snapshot_event_id FROM memory_sync_snapshots WHERE memory_id = ? AND revision = 1`, candidate.memoryID).Scan(&payload.CreatedEventID); err != nil {
			return fmt.Errorf("read memory candidate snapshot: %w", err)
		}
	}
	snapshot, err := s.memoryEvent(source.VaultID, memorySnapshotEventType, payload, source.OccurredAt)
	if err != nil {
		return err
	}
	if _, err := memoryFromEvent(snapshot); err != nil {
		return fmt.Errorf("validate memory sync snapshot: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT snapshot_event_id FROM memory_sync_snapshots WHERE memory_id = ? AND revision = ?`, candidate.memoryID, candidate.revision).Scan(&existing)
	if err == nil {
		return nil
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err := s.insertEvent(ctx, tx, snapshot); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO memory_sync_snapshots(memory_id, revision, source_event_id, snapshot_event_id, created_at) VALUES(?, ?, ?, ?, ?)`, candidate.memoryID, candidate.revision, candidate.sourceEventID, snapshot.ID, s.now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}
