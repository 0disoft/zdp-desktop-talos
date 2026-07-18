package sqliteevent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspacemapping"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/workspacestore"
)

const workspaceMappingEventSchemaVersion = 1

type workspaceMappingPayload struct {
	WorkspaceID         string                 `json:"workspace_id"`
	VaultID             string                 `json:"vault_id"`
	SourceWorkspaceHash string                 `json:"source_workspace_hash"`
	LocalRoot           string                 `json:"local_root"`
	LocalRootHash       string                 `json:"local_root_hash"`
	VerifiedBaseline    string                 `json:"verified_baseline"`
	State               workspacemapping.State `json:"state"`
	Revision            int                    `json:"revision"`
	CreatedAt           string                 `json:"created_at"`
	UpdatedAt           string                 `json:"updated_at"`
}

type workspaceMappingPointer struct {
	workspaceID, vaultID, sourceWorkspaceHash, localRootHash, verifiedBaseline string
	state                                                                      workspacemapping.State
	revision                                                                   int
	createdAt, updatedAt, createdEventID, lastEventID                          string
}

func (s *Store) GetTaskWorkspace(ctx context.Context, vaultID, taskID string) (workspacemapping.Requirement, error) {
	if vaultID == "" || taskID == "" {
		return workspacemapping.Requirement{}, workspacestore.ErrInvalidCommand
	}
	pointer, err := scanTaskPointer(s.db.QueryRowContext(ctx, taskSelect+" WHERE task_id = ? AND vault_id = ?", taskID, vaultID))
	if err != nil {
		return workspacemapping.Requirement{}, err
	}
	workspaceID := pointer.workspaceID
	if workspaceID == "" {
		workspaceID = workspacemapping.ID(pointer.vaultID, pointer.workspaceRootHash)
	}
	requirement := workspacemapping.Requirement{WorkspaceID: workspaceID, VaultID: pointer.vaultID, SourceWorkspaceHash: pointer.workspaceRootHash, BaselineCommit: pointer.baselineCommit}
	mapping, err := s.GetWorkspaceMapping(ctx, pointer.vaultID, workspaceID)
	if err == nil && mapping.State == workspacemapping.StateActive {
		requirement.Mapped = true
		requirement.Mapping = mapping
	} else if err != nil && !errors.Is(err, workspacestore.ErrNotFound) {
		return workspacemapping.Requirement{}, err
	}
	if requirement.Validate() != nil {
		return workspacemapping.Requirement{}, workspacestore.ErrConflict
	}
	return requirement, nil
}

func (s *Store) GetWorkspaceMapping(ctx context.Context, vaultID, workspaceID string) (workspacemapping.Record, error) {
	pointer, err := scanWorkspaceMappingPointer(s.db.QueryRowContext(ctx, workspaceMappingSelect+" WHERE vault_id = ? AND workspace_id = ?", vaultID, workspaceID))
	if err != nil {
		return workspacemapping.Record{}, err
	}
	return s.workspaceMappingFromPointer(ctx, s.db, pointer)
}

func (s *Store) BindWorkspaceMapping(ctx context.Context, input workspacestore.BindInput) (workspacemapping.Record, bool, error) {
	localRoot, localRootHash, err := normalizedWorkspaceRoot(input.LocalRoot)
	if err != nil || input.WorkspaceID != workspacemapping.ID(input.VaultID, input.SourceWorkspaceHash) || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 || input.ExpectedRevision < 0 {
		return workspacemapping.Record{}, false, workspacestore.ErrInvalidCommand
	}
	input.LocalRoot = localRoot
	occurredAt := normalizedTime(input.OccurredAt, s.now)
	requestHash, err := workspaceMappingCommandHash(input)
	if err != nil {
		return workspacemapping.Record{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return workspacemapping.Record{}, false, fmt.Errorf("begin workspace mapping bind: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existingEvent, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, requestHash)
	if err != nil {
		return workspacemapping.Record{}, false, mapWorkspaceIdempotencyError(err)
	}
	if found {
		record, err := s.workspaceMappingFromReplayEvent(ctx, tx, existingEvent)
		return record, true, err
	}
	if err := requireActiveVault(ctx, tx, input.VaultID); err != nil {
		return workspacemapping.Record{}, false, err
	}
	pointer, exists, err := getWorkspaceMappingPointer(ctx, tx, input.VaultID, input.WorkspaceID)
	if err != nil {
		return workspacemapping.Record{}, false, err
	}
	if !exists {
		if input.ExpectedRevision != 0 {
			return workspacemapping.Record{}, false, workspacestore.ErrRevisionConflict
		}
		record := workspacemapping.Record{WorkspaceID: input.WorkspaceID, VaultID: input.VaultID, SourceWorkspaceHash: input.SourceWorkspaceHash, LocalRoot: localRoot, LocalRootHash: localRootHash, VerifiedBaseline: input.VerifiedBaseline, State: workspacemapping.StateActive, Revision: 1, CreatedAt: occurredAt, UpdatedAt: occurredAt, CreatedEventID: "pending", LastEventID: "pending"}
		if record.Validate() != nil {
			return workspacemapping.Record{}, false, workspacestore.ErrInvalidCommand
		}
		created, err := s.insertWorkspaceMapping(ctx, tx, "workspace.mapping.bound", record)
		if err != nil {
			return workspacemapping.Record{}, false, err
		}
		if err := claimIdempotency(ctx, tx, input.IdempotencyKey, created.LastEventID, requestHash); err != nil {
			return workspacemapping.Record{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return workspacemapping.Record{}, false, err
		}
		return created, false, nil
	}
	current, err := s.workspaceMappingFromPointer(ctx, tx, pointer)
	if err != nil {
		return workspacemapping.Record{}, false, err
	}
	if current.Revision != input.ExpectedRevision {
		return workspacemapping.Record{}, false, workspacestore.ErrRevisionConflict
	}
	if current.State == workspacemapping.StateActive && current.LocalRootHash == localRootHash && current.VerifiedBaseline == input.VerifiedBaseline {
		current.Revision++
		current.UpdatedAt = occurredAt
		confirmed, err := s.updateWorkspaceMapping(ctx, tx, "workspace.mapping.confirmed", current, input.ExpectedRevision)
		if err != nil {
			return workspacemapping.Record{}, false, err
		}
		if err := claimIdempotency(ctx, tx, input.IdempotencyKey, confirmed.LastEventID, requestHash); err != nil {
			return workspacemapping.Record{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return workspacemapping.Record{}, false, err
		}
		return confirmed, false, nil
	}
	current.LocalRoot = localRoot
	current.LocalRootHash = localRootHash
	current.VerifiedBaseline = input.VerifiedBaseline
	current.State = workspacemapping.StateActive
	current.Revision++
	current.UpdatedAt = occurredAt
	updated, err := s.updateWorkspaceMapping(ctx, tx, "workspace.mapping.remapped", current, input.ExpectedRevision)
	if err != nil {
		return workspacemapping.Record{}, false, err
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, updated.LastEventID, requestHash); err != nil {
		return workspacemapping.Record{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return workspacemapping.Record{}, false, err
	}
	return updated, false, nil
}

func (s *Store) RevokeWorkspaceMapping(ctx context.Context, input workspacestore.RevokeInput) (workspacemapping.Record, bool, error) {
	if input.VaultID == "" || !workspacemapping.ValidWorkspaceID(input.WorkspaceID) || input.ExpectedRevision < 1 || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 {
		return workspacemapping.Record{}, false, workspacestore.ErrInvalidCommand
	}
	occurredAt := normalizedTime(input.OccurredAt, s.now)
	requestHash, err := workspaceMappingCommandHash(input)
	if err != nil {
		return workspacemapping.Record{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return workspacemapping.Record{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	existingEvent, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, requestHash)
	if err != nil {
		return workspacemapping.Record{}, false, mapWorkspaceIdempotencyError(err)
	}
	if found {
		record, err := s.workspaceMappingFromReplayEvent(ctx, tx, existingEvent)
		return record, true, err
	}
	if err := requireActiveVault(ctx, tx, input.VaultID); err != nil {
		return workspacemapping.Record{}, false, err
	}
	pointer, exists, err := getWorkspaceMappingPointer(ctx, tx, input.VaultID, input.WorkspaceID)
	if err != nil {
		return workspacemapping.Record{}, false, err
	}
	if !exists {
		return workspacemapping.Record{}, false, workspacestore.ErrNotFound
	}
	current, err := s.workspaceMappingFromPointer(ctx, tx, pointer)
	if err != nil {
		return workspacemapping.Record{}, false, err
	}
	if current.Revision != input.ExpectedRevision || current.State != workspacemapping.StateActive {
		return workspacemapping.Record{}, false, workspacestore.ErrRevisionConflict
	}
	current.State = workspacemapping.StateRevoked
	current.Revision++
	current.UpdatedAt = occurredAt
	updated, err := s.updateWorkspaceMapping(ctx, tx, "workspace.mapping.revoked", current, input.ExpectedRevision)
	if err != nil {
		return workspacemapping.Record{}, false, err
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, updated.LastEventID, requestHash); err != nil {
		return workspacemapping.Record{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return workspacemapping.Record{}, false, err
	}
	return updated, false, nil
}

func (s *Store) ensureLocalWorkspaceMapping(ctx context.Context, tx *sql.Tx, vaultID, localRoot, baseline string, occurredAt time.Time) (workspacemapping.Record, error) {
	localRoot, localHash, err := normalizedWorkspaceRoot(localRoot)
	if err != nil {
		return workspacemapping.Record{}, workspacestore.ErrInvalidCommand
	}
	pointer, exists, err := getWorkspaceMappingByLocalHash(ctx, tx, vaultID, localHash)
	if err != nil {
		return workspacemapping.Record{}, err
	}
	if exists {
		current, err := s.workspaceMappingFromPointer(ctx, tx, pointer)
		if err != nil || current.State != workspacemapping.StateActive || current.LocalRoot != localRoot {
			return workspacemapping.Record{}, workspacestore.ErrConflict
		}
		return current, nil
	}
	sourceHash := localHash
	record := workspacemapping.Record{WorkspaceID: workspacemapping.ID(vaultID, sourceHash), VaultID: vaultID, SourceWorkspaceHash: sourceHash, LocalRoot: localRoot, LocalRootHash: localHash, VerifiedBaseline: baseline, State: workspacemapping.StateActive, Revision: 1, CreatedAt: occurredAt, UpdatedAt: occurredAt, CreatedEventID: "pending", LastEventID: "pending"}
	if record.Validate() != nil {
		return workspacemapping.Record{}, workspacestore.ErrInvalidCommand
	}
	return s.insertWorkspaceMapping(ctx, tx, "workspace.mapping.bound", record)
}

func (s *Store) resolveWorkspaceRoot(ctx context.Context, queryer workspaceMappingQueryer, vaultID, workspaceID string) (string, error) {
	pointer, err := scanWorkspaceMappingPointer(queryer.QueryRowContext(ctx, workspaceMappingSelect+" WHERE vault_id = ? AND workspace_id = ?", vaultID, workspaceID))
	if err != nil {
		if errors.Is(err, workspacestore.ErrNotFound) {
			return "", workspacestore.ErrMappingRequired
		}
		return "", err
	}
	record, err := s.workspaceMappingFromPointer(ctx, queryer, pointer)
	if err != nil {
		return "", err
	}
	if record.State != workspacemapping.StateActive {
		return "", workspacestore.ErrMappingRequired
	}
	return record.LocalRoot, nil
}

func (s *Store) ReconcileWorkspaceMappings(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT t.task_id, t.vault_id, t.workspace_root_hash, t.baseline_commit, r.event_id, COALESCE(o.origin_kind, 'local') FROM tasks t JOIN task_contract_revisions r ON r.task_id = t.task_id AND r.revision = 1 LEFT JOIN sync_event_origins o ON o.event_id = r.event_id WHERE t.workspace_id = '' ORDER BY t.created_at, t.task_id`)
	if err != nil {
		return fmt.Errorf("list workspace mapping backfill: %w", err)
	}
	type candidate struct{ taskID, vaultID, sourceHash, baseline, eventID, origin string }
	var candidates []candidate
	for rows.Next() {
		var current candidate
		if err := rows.Scan(&current.taskID, &current.vaultID, &current.sourceHash, &current.baseline, &current.eventID, &current.origin); err != nil {
			_ = rows.Close()
			return err
		}
		candidates = append(candidates, current)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, current := range candidates {
		record, err := s.Get(ctx, current.eventID)
		if err != nil {
			return err
		}
		var payload taskContractPayload
		if record.SchemaVersion != taskContractLegacyEventSchemaVersion || json.Unmarshal(record.Payload, &payload) != nil || workspaceRootHash(payload.WorkspaceRoot) != current.sourceHash {
			return fmt.Errorf("backfill workspace mapping: task event is incompatible")
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		workspaceID := workspacemapping.ID(current.vaultID, current.sourceHash)
		if current.origin != "imported" {
			if _, err := s.ensureLocalWorkspaceMapping(ctx, tx, current.vaultID, payload.WorkspaceRoot, current.baseline, s.now()); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET workspace_id = ? WHERE task_id = ? AND workspace_id = ''`, workspaceID, current.taskID); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) insertWorkspaceMapping(ctx context.Context, tx *sql.Tx, eventType string, record workspacemapping.Record) (workspacemapping.Record, error) {
	payload := workspaceMappingPayloadFromRecord(record)
	eventRecord, err := s.workspaceMappingEvent(eventType, record.VaultID, payload, record.UpdatedAt)
	if err != nil {
		return workspacemapping.Record{}, err
	}
	if err := s.insertEvent(ctx, tx, eventRecord); err != nil {
		return workspacemapping.Record{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_mappings(workspace_id, vault_id, source_workspace_hash, local_root_hash, verified_baseline, state, revision, created_at, updated_at, created_event_id, last_event_id) VALUES(?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?)`, record.WorkspaceID, record.VaultID, record.SourceWorkspaceHash, record.LocalRootHash, record.VerifiedBaseline, string(record.State), payload.CreatedAt, payload.UpdatedAt, eventRecord.ID, eventRecord.ID); err != nil {
		return workspacemapping.Record{}, mapWorkspaceConstraintError(err)
	}
	record.CreatedEventID = eventRecord.ID
	record.LastEventID = eventRecord.ID
	return record, nil
}

func (s *Store) updateWorkspaceMapping(ctx context.Context, tx *sql.Tx, eventType string, record workspacemapping.Record, expectedRevision int) (workspacemapping.Record, error) {
	if record.Validate() != nil {
		return workspacemapping.Record{}, workspacestore.ErrInvalidCommand
	}
	payload := workspaceMappingPayloadFromRecord(record)
	eventRecord, err := s.workspaceMappingEvent(eventType, record.VaultID, payload, record.UpdatedAt)
	if err != nil {
		return workspacemapping.Record{}, err
	}
	if err := s.insertEvent(ctx, tx, eventRecord); err != nil {
		return workspacemapping.Record{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE workspace_mappings SET local_root_hash = ?, verified_baseline = ?, state = ?, revision = ?, updated_at = ?, last_event_id = ? WHERE vault_id = ? AND workspace_id = ? AND revision = ?`, record.LocalRootHash, record.VerifiedBaseline, string(record.State), record.Revision, payload.UpdatedAt, eventRecord.ID, record.VaultID, record.WorkspaceID, expectedRevision)
	if err != nil {
		return workspacemapping.Record{}, mapWorkspaceConstraintError(err)
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return workspacemapping.Record{}, workspacestore.ErrRevisionConflict
	}
	record.LastEventID = eventRecord.ID
	return record, nil
}

func (s *Store) workspaceMappingFromPointer(ctx context.Context, queryer workspaceMappingQueryer, pointer workspaceMappingPointer) (workspacemapping.Record, error) {
	eventRecord, err := s.getEventWithQueryer(ctx, queryer, pointer.lastEventID)
	if err != nil {
		return workspacemapping.Record{}, err
	}
	record, err := workspaceMappingFromEvent(eventRecord)
	if err != nil {
		return workspacemapping.Record{}, err
	}
	record.CreatedEventID = pointer.createdEventID
	if record.Validate() != nil || record.WorkspaceID != pointer.workspaceID || record.VaultID != pointer.vaultID || record.SourceWorkspaceHash != pointer.sourceWorkspaceHash || record.LocalRootHash != pointer.localRootHash || record.VerifiedBaseline != pointer.verifiedBaseline || record.State != pointer.state || record.Revision != pointer.revision || record.CreatedAt.Format(time.RFC3339Nano) != pointer.createdAt || record.UpdatedAt.Format(time.RFC3339Nano) != pointer.updatedAt || record.LastEventID != pointer.lastEventID {
		return workspacemapping.Record{}, workspacestore.ErrConflict
	}
	return record, nil
}

func (s *Store) workspaceMappingFromReplayEvent(ctx context.Context, queryer workspaceMappingQueryer, eventRecord event.Record) (workspacemapping.Record, error) {
	record, err := workspaceMappingFromEvent(eventRecord)
	if err != nil {
		return workspacemapping.Record{}, err
	}
	var createdEventID string
	if err := queryer.QueryRowContext(ctx, `SELECT created_event_id FROM workspace_mappings WHERE vault_id = ? AND workspace_id = ?`, record.VaultID, record.WorkspaceID).Scan(&createdEventID); err != nil {
		return workspacemapping.Record{}, err
	}
	record.CreatedEventID = createdEventID
	if record.Validate() != nil {
		return workspacemapping.Record{}, workspacestore.ErrConflict
	}
	return record, nil
}

func workspaceMappingFromEvent(record event.Record) (workspacemapping.Record, error) {
	if record.SchemaVersion != workspaceMappingEventSchemaVersion || record.Sensitivity != event.SensitivityPrivate || record.Type != "workspace.mapping.bound" && record.Type != "workspace.mapping.confirmed" && record.Type != "workspace.mapping.remapped" && record.Type != "workspace.mapping.revoked" {
		return workspacemapping.Record{}, workspacestore.ErrConflict
	}
	var payload workspaceMappingPayload
	if json.Unmarshal(record.Payload, &payload) != nil || payload.VaultID != record.VaultID {
		return workspacemapping.Record{}, workspacestore.ErrConflict
	}
	createdAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
	if err != nil {
		return workspacemapping.Record{}, workspacestore.ErrConflict
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, payload.UpdatedAt)
	if err != nil {
		return workspacemapping.Record{}, workspacestore.ErrConflict
	}
	result := workspacemapping.Record{WorkspaceID: payload.WorkspaceID, VaultID: payload.VaultID, SourceWorkspaceHash: payload.SourceWorkspaceHash, LocalRoot: payload.LocalRoot, LocalRootHash: payload.LocalRootHash, VerifiedBaseline: payload.VerifiedBaseline, State: payload.State, Revision: payload.Revision, CreatedAt: createdAt, UpdatedAt: updatedAt, LastEventID: record.ID}
	result.CreatedEventID = record.ID
	if !workspacemapping.ValidWorkspaceID(result.WorkspaceID) || result.VaultID == "" || result.SourceWorkspaceHash == "" || result.LocalRootHash == "" || result.VerifiedBaseline == "" || result.Revision < 1 || result.CreatedAt.IsZero() || result.UpdatedAt.Before(result.CreatedAt) || result.LastEventID == "" {
		return workspacemapping.Record{}, workspacestore.ErrConflict
	}
	return result, nil
}

func workspaceMappingPayloadFromRecord(record workspacemapping.Record) workspaceMappingPayload {
	return workspaceMappingPayload{WorkspaceID: record.WorkspaceID, VaultID: record.VaultID, SourceWorkspaceHash: record.SourceWorkspaceHash, LocalRoot: record.LocalRoot, LocalRootHash: record.LocalRootHash, VerifiedBaseline: record.VerifiedBaseline, State: record.State, Revision: record.Revision, CreatedAt: record.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: record.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}

func (s *Store) workspaceMappingEvent(eventType, vaultID string, payload workspaceMappingPayload, occurredAt time.Time) (event.Record, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return event.Record{}, err
	}
	return s.newEventRecord(vaultID, eventType, workspaceMappingEventSchemaVersion, event.SensitivityPrivate, encoded, occurredAt)
}

const workspaceMappingSelect = `SELECT workspace_id, vault_id, source_workspace_hash, local_root_hash, verified_baseline, state, revision, created_at, updated_at, created_event_id, last_event_id FROM workspace_mappings`

type workspaceMappingQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func scanWorkspaceMappingPointer(row scanner) (workspaceMappingPointer, error) {
	var pointer workspaceMappingPointer
	var state string
	if err := row.Scan(&pointer.workspaceID, &pointer.vaultID, &pointer.sourceWorkspaceHash, &pointer.localRootHash, &pointer.verifiedBaseline, &state, &pointer.revision, &pointer.createdAt, &pointer.updatedAt, &pointer.createdEventID, &pointer.lastEventID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return workspaceMappingPointer{}, workspacestore.ErrNotFound
		}
		return workspaceMappingPointer{}, err
	}
	pointer.state = workspacemapping.State(state)
	return pointer, nil
}

func getWorkspaceMappingPointer(ctx context.Context, queryer workspaceMappingQueryer, vaultID, workspaceID string) (workspaceMappingPointer, bool, error) {
	pointer, err := scanWorkspaceMappingPointer(queryer.QueryRowContext(ctx, workspaceMappingSelect+" WHERE vault_id = ? AND workspace_id = ?", vaultID, workspaceID))
	if errors.Is(err, workspacestore.ErrNotFound) {
		return workspaceMappingPointer{}, false, nil
	}
	return pointer, err == nil, err
}

func getWorkspaceMappingByLocalHash(ctx context.Context, queryer workspaceMappingQueryer, vaultID, localRootHash string) (workspaceMappingPointer, bool, error) {
	pointer, err := scanWorkspaceMappingPointer(queryer.QueryRowContext(ctx, workspaceMappingSelect+" WHERE vault_id = ? AND local_root_hash = ? AND state = 'active'", vaultID, localRootHash))
	if errors.Is(err, workspacestore.ErrNotFound) {
		return workspaceMappingPointer{}, false, nil
	}
	return pointer, err == nil, err
}

func (s *Store) getEventWithQueryer(ctx context.Context, queryer workspaceMappingQueryer, eventID string) (event.Record, error) {
	var record event.Record
	var sensitivity, occurredAt string
	var encrypted []byte
	if err := queryer.QueryRowContext(ctx, `SELECT event_id, vault_id, event_type, schema_version, sensitivity, payload_envelope, occurred_at FROM events WHERE event_id = ?`, eventID).Scan(&record.ID, &record.VaultID, &record.Type, &record.SchemaVersion, &sensitivity, &encrypted, &occurredAt); err != nil {
		return event.Record{}, err
	}
	record.Sensitivity = event.Sensitivity(sensitivity)
	record.OccurredAt, _ = time.Parse(time.RFC3339Nano, occurredAt)
	payload, err := s.sealer.Open(encrypted, associatedData(record))
	if err != nil {
		return event.Record{}, err
	}
	record.Payload = payload
	return record, nil
}

func normalizedWorkspaceRoot(root string) (string, string, error) {
	if root == "" || !filepath.IsAbs(root) {
		return "", "", workspacestore.ErrInvalidCommand
	}
	clean := filepath.Clean(root)
	return clean, workspacemapping.RootHash(clean), nil
}

func workspaceMappingCommandHash(input any) ([]byte, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	return hash[:], nil
}

func mapWorkspaceIdempotencyError(err error) error {
	if errors.Is(err, ErrIdempotencyConflict) || errors.Is(err, ErrIdempotencyUnverifiable) {
		return workspacestore.ErrConflict
	}
	return err
}

func mapWorkspaceConstraintError(err error) error {
	if err == nil {
		return nil
	}
	if message := err.Error(); len(message) > 0 && (containsConstraint(message, "UNIQUE constraint failed") || containsConstraint(message, "CHECK constraint failed")) {
		return workspacestore.ErrConflict
	}
	return err
}

func containsConstraint(message, needle string) bool {
	return len(message) >= len(needle) && (message[:len(needle)] == needle || stringContains(message, needle))
}

func stringContains(value, needle string) bool {
	for index := 0; index+len(needle) <= len(value); index++ {
		if value[index:index+len(needle)] == needle {
			return true
		}
	}
	return false
}
