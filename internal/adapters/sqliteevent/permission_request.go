package sqliteevent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/permission"
	"github.com/0disoft/zdp-desktop-talos/internal/id"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/executionstore"
)

const permissionRequestEventSchemaVersion = 1

type permissionRequestPayload struct {
	RequestID string                   `json:"request_id"`
	Intent    permission.ProcessIntent `json:"intent"`
	CreatedAt string                   `json:"created_at"`
}

type permissionResolutionPayload struct {
	RequestID  string             `json:"request_id"`
	GrantID    string             `json:"grant_id"`
	Outcome    permission.Outcome `json:"outcome"`
	ExpiresAt  string             `json:"expires_at,omitempty"`
	ResolvedAt string             `json:"resolved_at"`
}

type permissionRequestPointer struct {
	requestID, vaultID, taskID, workspaceHash, capabilityHash string
	state                                                     permission.RequestState
	createdAt, updatedAt                                      time.Time
	createdEventID, lastEventID                               string
}

func (s *Store) CreatePermissionRequest(ctx context.Context, input executionstore.CreatePermissionRequestInput) (permission.Request, error) {
	occurredAt := normalizedTime(input.OccurredAt, s.now)
	if input.VaultID == "" || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 || input.Intent.Validate() != nil {
		return permission.Request{}, executionstore.ErrInvalidCommand
	}
	workspaceHash, err := permission.WorkspaceHash(input.Intent.WorkspaceRoot)
	if err != nil {
		return permission.Request{}, executionstore.ErrInvalidCommand
	}
	capabilityHash, err := permission.IntentHash(input.Intent)
	if err != nil {
		return permission.Request{}, executionstore.ErrInvalidCommand
	}
	requestHash, err := hashExecutionCommand(input)
	if err != nil {
		return permission.Request{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return permission.Request{}, fmt.Errorf("begin permission request: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existing, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, requestHash)
	if err != nil {
		return permission.Request{}, mapExecutionIdempotencyError(err)
	}
	if found {
		return s.permissionRequestByCreatedEvent(ctx, tx, existing.ID)
	}
	if err := requireActiveVault(ctx, tx, input.VaultID); err != nil {
		return permission.Request{}, err
	}
	pointer, err := scanTaskPointer(tx.QueryRowContext(ctx, taskSelect+" WHERE task_id = ? AND vault_id = ?", input.Intent.TaskID, input.VaultID))
	if err != nil {
		return permission.Request{}, executionstore.ErrNotFound
	}
	taskEvent, err := s.getEventInTx(tx, pointer.lastEventID)
	if err != nil {
		return permission.Request{}, err
	}
	taskRecord, err := taskFromEvent(taskEvent, pointer)
	if err != nil || taskRecord.WorkspaceRoot != input.Intent.WorkspaceRoot {
		return permission.Request{}, executionstore.ErrConflict
	}
	requestID, err := id.UUIDv7(occurredAt, s.random)
	if err != nil {
		return permission.Request{}, fmt.Errorf("generate permission request id: %w", err)
	}
	payload := permissionRequestPayload{RequestID: requestID, Intent: input.Intent, CreatedAt: occurredAt.Format(time.RFC3339Nano)}
	record, err := s.permissionRequestEvent(input.VaultID, "permission.request.created", payload, occurredAt)
	if err != nil {
		return permission.Request{}, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return permission.Request{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO permission_requests(request_id, vault_id, task_id, workspace_hash, capability_hash, state, created_at, updated_at, created_event_id, last_event_id) VALUES(?, ?, ?, ?, ?, 'open', ?, ?, ?, ?)`, requestID, input.VaultID, input.Intent.TaskID, workspaceHash, capabilityHash, payload.CreatedAt, payload.CreatedAt, record.ID, record.ID); err != nil {
		return permission.Request{}, fmt.Errorf("insert permission request: %w", err)
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, record.ID, requestHash); err != nil {
		return permission.Request{}, err
	}
	if err := tx.Commit(); err != nil {
		return permission.Request{}, fmt.Errorf("commit permission request: %w", err)
	}
	return s.getPermissionRequest(ctx, requestID)
}

func (s *Store) ListOpenPermissionRequests(ctx context.Context, vaultID, taskID string, limit int) ([]permission.Request, error) {
	if vaultID == "" || taskID == "" || limit < 1 || limit > 256 {
		return nil, executionstore.ErrInvalidCommand
	}
	rows, err := s.db.QueryContext(ctx, permissionRequestSelect+" WHERE vault_id = ? AND task_id = ? AND state = 'open' ORDER BY created_at, request_id LIMIT ?", vaultID, taskID, limit)
	if err != nil {
		return nil, fmt.Errorf("list permission requests: %w", err)
	}
	defer rows.Close()
	pointers := make([]permissionRequestPointer, 0)
	for rows.Next() {
		pointer, err := scanPermissionRequestPointer(rows)
		if err != nil {
			return nil, err
		}
		pointers = append(pointers, pointer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate permission requests: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close permission request rows: %w", err)
	}
	requests := make([]permission.Request, 0, len(pointers))
	for _, pointer := range pointers {
		request, err := s.permissionRequestFromPointer(ctx, pointer)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, nil
}

func (s *Store) ResolvePermissionRequest(ctx context.Context, input executionstore.ResolvePermissionRequestInput) (executionstore.PermissionResolution, error) {
	occurredAt := normalizedTime(input.OccurredAt, s.now)
	if input.VaultID == "" || input.RequestID == "" || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 || !resolvablePermissionOutcome(input.Outcome) {
		return executionstore.PermissionResolution{}, executionstore.ErrInvalidCommand
	}
	requestHash, err := hashExecutionCommand(input)
	if err != nil {
		return executionstore.PermissionResolution{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return executionstore.PermissionResolution{}, fmt.Errorf("begin permission resolution: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existing, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, requestHash)
	if err != nil {
		return executionstore.PermissionResolution{}, mapExecutionIdempotencyError(err)
	}
	if found {
		return s.permissionResolutionByEvent(ctx, tx, existing.ID)
	}
	pointer, err := scanPermissionRequestPointer(tx.QueryRowContext(ctx, permissionRequestSelect+" WHERE request_id = ? AND vault_id = ?", input.RequestID, input.VaultID))
	if err != nil {
		return executionstore.PermissionResolution{}, err
	}
	if pointer.state != permission.RequestOpen || occurredAt.Before(pointer.createdAt) {
		return executionstore.PermissionResolution{}, executionstore.ErrConflict
	}
	request, err := s.permissionRequestFromPointerTx(tx, pointer)
	if err != nil {
		return executionstore.PermissionResolution{}, err
	}
	capabilityHash := request.CapabilityHash
	taskID := request.TaskID
	if input.Outcome == permission.OutcomeAllowWorkspace {
		capabilityHash, err = permission.WorkspaceIntentHash(request.Intent)
		taskID = ""
		if err != nil {
			return executionstore.PermissionResolution{}, executionstore.ErrConflict
		}
	}
	if input.Outcome == permission.OutcomeDeny {
		taskID = request.TaskID
	}
	grantID, err := id.UUIDv7(occurredAt, s.random)
	if err != nil {
		return executionstore.PermissionResolution{}, fmt.Errorf("generate permission grant id: %w", err)
	}
	grant := permission.Grant{ID: grantID, Outcome: input.Outcome, State: permission.GrantActive, CapabilityHash: capabilityHash, TaskID: taskID, WorkspaceHash: request.WorkspaceHash, CreatedAt: occurredAt, ExpiresAt: input.ExpiresAt.UTC()}
	if grant.Validate() != nil {
		return executionstore.PermissionResolution{}, executionstore.ErrInvalidCommand
	}
	payload := permissionResolutionPayload{RequestID: request.ID, GrantID: grant.ID, Outcome: grant.Outcome, ResolvedAt: occurredAt.Format(time.RFC3339Nano)}
	if !grant.ExpiresAt.IsZero() {
		payload.ExpiresAt = grant.ExpiresAt.Format(time.RFC3339Nano)
	}
	record, err := s.permissionRequestEvent(input.VaultID, "permission.request.resolved", payload, occurredAt)
	if err != nil {
		return executionstore.PermissionResolution{}, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return executionstore.PermissionResolution{}, err
	}
	var nullableTaskID any
	if grant.TaskID != "" {
		nullableTaskID = grant.TaskID
	}
	var nullableExpiry any
	if !grant.ExpiresAt.IsZero() {
		nullableExpiry = payload.ExpiresAt
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO permission_grants(grant_id, vault_id, outcome, state, capability_hash, task_id, workspace_hash, created_at, expires_at, created_event_id, last_event_id) VALUES(?, ?, ?, 'active', ?, ?, ?, ?, ?, ?, ?)`, grant.ID, input.VaultID, string(grant.Outcome), grant.CapabilityHash, nullableTaskID, grant.WorkspaceHash, payload.ResolvedAt, nullableExpiry, record.ID, record.ID); err != nil {
		return executionstore.PermissionResolution{}, fmt.Errorf("insert resolved permission grant: %w", err)
	}
	nextState := permission.RequestApproved
	if input.Outcome == permission.OutcomeDeny {
		nextState = permission.RequestDenied
	}
	result, err := tx.ExecContext(ctx, `UPDATE permission_requests SET state = ?, updated_at = ?, last_event_id = ? WHERE request_id = ? AND vault_id = ? AND state = 'open'`, string(nextState), payload.ResolvedAt, record.ID, request.ID, input.VaultID)
	if err != nil {
		return executionstore.PermissionResolution{}, fmt.Errorf("resolve permission request: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return executionstore.PermissionResolution{}, executionstore.ErrConflict
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, record.ID, requestHash); err != nil {
		return executionstore.PermissionResolution{}, err
	}
	if err := tx.Commit(); err != nil {
		return executionstore.PermissionResolution{}, fmt.Errorf("commit permission resolution: %w", err)
	}
	resolved, err := s.getPermissionRequest(ctx, request.ID)
	return executionstore.PermissionResolution{Request: resolved, Grant: grant}, err
}

func resolvablePermissionOutcome(outcome permission.Outcome) bool {
	return outcome == permission.OutcomeDeny || outcome == permission.OutcomeAllowOnce || outcome == permission.OutcomeAllowTask || outcome == permission.OutcomeAllowWorkspace
}

func (s *Store) permissionRequestEvent(vaultID, eventType string, payload any, occurredAt time.Time) (event.Record, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return event.Record{}, fmt.Errorf("encode permission request event: %w", err)
	}
	return s.newEventRecord(vaultID, eventType, permissionRequestEventSchemaVersion, event.SensitivityPrivate, encoded, occurredAt)
}

func (s *Store) getPermissionRequest(ctx context.Context, requestID string) (permission.Request, error) {
	pointer, err := scanPermissionRequestPointer(s.db.QueryRowContext(ctx, permissionRequestSelect+" WHERE request_id = ?", requestID))
	if err != nil {
		return permission.Request{}, err
	}
	return s.permissionRequestFromPointer(ctx, pointer)
}

func (s *Store) permissionRequestFromPointer(ctx context.Context, pointer permissionRequestPointer) (permission.Request, error) {
	record, err := s.Get(ctx, pointer.createdEventID)
	return permissionRequestFromEvent(pointer, record, err)
}

func (s *Store) permissionRequestFromPointerTx(tx *sql.Tx, pointer permissionRequestPointer) (permission.Request, error) {
	record, err := s.getEventInTx(tx, pointer.createdEventID)
	return permissionRequestFromEvent(pointer, record, err)
}

func permissionRequestFromEvent(pointer permissionRequestPointer, record event.Record, err error) (permission.Request, error) {
	if err != nil || record.Type != "permission.request.created" {
		return permission.Request{}, executionstore.ErrConflict
	}
	var payload permissionRequestPayload
	if json.Unmarshal(record.Payload, &payload) != nil || payload.RequestID != pointer.requestID {
		return permission.Request{}, executionstore.ErrConflict
	}
	request := permission.Request{ID: pointer.requestID, VaultID: pointer.vaultID, TaskID: pointer.taskID, WorkspaceHash: pointer.workspaceHash, CapabilityHash: pointer.capabilityHash, Intent: payload.Intent, State: pointer.state, CreatedAt: pointer.createdAt, UpdatedAt: pointer.updatedAt, LastEventID: pointer.lastEventID}
	if request.Validate() != nil {
		return permission.Request{}, executionstore.ErrConflict
	}
	return request, nil
}

func (s *Store) permissionRequestByCreatedEvent(ctx context.Context, tx *sql.Tx, eventID string) (permission.Request, error) {
	pointer, err := scanPermissionRequestPointer(tx.QueryRowContext(ctx, permissionRequestSelect+" WHERE created_event_id = ?", eventID))
	if err != nil {
		return permission.Request{}, err
	}
	return s.permissionRequestFromPointerTx(tx, pointer)
}

func (s *Store) permissionResolutionByEvent(ctx context.Context, tx *sql.Tx, eventID string) (executionstore.PermissionResolution, error) {
	pointer, err := scanPermissionRequestPointer(tx.QueryRowContext(ctx, permissionRequestSelect+" WHERE last_event_id = ?", eventID))
	if err != nil {
		return executionstore.PermissionResolution{}, err
	}
	request, err := s.permissionRequestFromPointerTx(tx, pointer)
	if err != nil {
		return executionstore.PermissionResolution{}, err
	}
	grant, err := scanPermissionGrant(tx.QueryRowContext(ctx, permissionGrantSelect+" WHERE created_event_id = ?", eventID))
	return executionstore.PermissionResolution{Request: request, Grant: grant}, err
}

func scanPermissionRequestPointer(row scanner) (permissionRequestPointer, error) {
	var pointer permissionRequestPointer
	var state, createdAt, updatedAt string
	if err := row.Scan(&pointer.requestID, &pointer.vaultID, &pointer.taskID, &pointer.workspaceHash, &pointer.capabilityHash, &state, &createdAt, &updatedAt, &pointer.createdEventID, &pointer.lastEventID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return permissionRequestPointer{}, executionstore.ErrNotFound
		}
		return permissionRequestPointer{}, fmt.Errorf("scan permission request: %w", err)
	}
	pointer.state = permission.RequestState(state)
	var err error
	pointer.createdAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err == nil {
		pointer.updatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	}
	if err != nil {
		return permissionRequestPointer{}, executionstore.ErrConflict
	}
	return pointer, nil
}

const permissionRequestSelect = `SELECT request_id, vault_id, task_id, workspace_hash, capability_hash, state, created_at, updated_at, created_event_id, last_event_id FROM permission_requests`
