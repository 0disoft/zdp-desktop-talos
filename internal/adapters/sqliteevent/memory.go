package sqliteevent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspacemapping"
	"github.com/0disoft/zdp-desktop-talos/internal/id"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorystore"
)

const (
	memoryCandidateCreatedEventType = "memory.candidate.created"
	memoryStateChangedEventType     = "memory.state.changed"
	memorySnapshotEventType         = "memory.record.snapshot"
	memoryLegacyEventSchemaVersion  = 1
	memoryEventSchemaVersion        = 2
)

type memoryPayload struct {
	MemoryID         string               `json:"memory_id"`
	Kind             memory.Kind          `json:"kind"`
	State            memory.State         `json:"state"`
	Scope            memory.Scope         `json:"scope"`
	Statement        string               `json:"statement"`
	Rationale        string               `json:"rationale"`
	Applicability    memory.Applicability `json:"applicability"`
	EvidenceEventIDs []string             `json:"evidence_event_ids"`
	SourceActor      string               `json:"source_actor"`
	Confidence       int                  `json:"confidence"`
	Sensitivity      event.Sensitivity    `json:"sensitivity"`
	Revision         int                  `json:"revision"`
	CreatedAt        string               `json:"created_at"`
	UpdatedAt        string               `json:"updated_at"`
	ReviewedAt       string               `json:"reviewed_at,omitempty"`
	ExpiresAt        string               `json:"expires_at,omitempty"`
	SupersededBy     string               `json:"superseded_by,omitempty"`
	CreatedEventID   string               `json:"created_event_id"`
	TransitionReason string               `json:"transition_reason,omitempty"`
}

type memoryPointer struct {
	memoryID, vaultID, kind, state, scopeKind, workspaceRootHash, workspaceID, sensitivity string
	createdAt, updatedAt, reviewedAt, expiresAt, supersededBy, createdEventID, lastEventID string
	confidence, revision                                                                   int
}

func (s *Store) CreateMemoryCandidate(ctx context.Context, input memorystore.CreateCandidateInput) (memory.Record, error) {
	applicability, err := input.Applicability.Normalize()
	if err != nil {
		return memory.Record{}, memorystore.ErrInvalidCommand
	}
	input.Applicability = applicability
	occurredAt := input.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = s.now()
	}
	occurredAt = occurredAt.UTC()
	validation := memory.Record{
		ID: "validation-memory", VaultID: input.VaultID, Kind: input.Kind, State: memory.StateCandidate,
		Scope: input.Scope, Statement: input.Statement, Rationale: input.Rationale, Applicability: input.Applicability,
		EvidenceEventIDs: input.EvidenceEventIDs, SourceActor: input.SourceActor, Confidence: input.Confidence,
		Sensitivity: input.Sensitivity, Revision: 1, CreatedAt: occurredAt, UpdatedAt: occurredAt,
		CreatedEventID: "validation-event", LastEventID: "validation-event",
	}
	if input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 || validation.Validate() != nil || validateMemoryScopeForSchema(input.VaultID, memoryEventSchemaVersion, input.Scope) != nil {
		return memory.Record{}, memorystore.ErrInvalidCommand
	}
	requestHash, err := memoryCommandHash(input)
	if err != nil {
		return memory.Record{}, fmt.Errorf("hash memory candidate command: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return memory.Record{}, fmt.Errorf("begin memory candidate transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existing, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, requestHash)
	if err != nil {
		return memory.Record{}, mapMemoryIdempotencyError(err)
	}
	if found {
		if existing.Type != memoryCandidateCreatedEventType {
			return memory.Record{}, memorystore.ErrIdempotencyConflict
		}
		return memoryFromEvent(existing)
	}
	if err := requireActiveVault(ctx, tx, input.VaultID); err != nil {
		return memory.Record{}, err
	}
	if err := requireMemoryEvidence(ctx, tx, input.VaultID, input.EvidenceEventIDs); err != nil {
		return memory.Record{}, err
	}
	memoryID, err := id.UUIDv7(occurredAt, s.random)
	if err != nil {
		return memory.Record{}, fmt.Errorf("generate memory id: %w", err)
	}
	payload := memoryPayload{
		MemoryID: memoryID, Kind: input.Kind, State: memory.StateCandidate, Scope: input.Scope,
		Statement: strings.TrimSpace(input.Statement), Rationale: strings.TrimSpace(input.Rationale), Applicability: applicability,
		EvidenceEventIDs: append([]string(nil), input.EvidenceEventIDs...), SourceActor: strings.TrimSpace(input.SourceActor),
		Confidence: input.Confidence, Sensitivity: input.Sensitivity, Revision: 1,
		CreatedAt: occurredAt.Format(time.RFC3339Nano), UpdatedAt: occurredAt.Format(time.RFC3339Nano),
	}
	eventRecord, err := s.memoryEvent(input.VaultID, memoryCandidateCreatedEventType, payload, occurredAt)
	if err != nil {
		return memory.Record{}, err
	}
	if err := s.insertEvent(ctx, tx, eventRecord); err != nil {
		return memory.Record{}, err
	}
	workspaceHash, workspaceID, err := memoryScopeIdentity(input.VaultID, payload.Scope)
	if err != nil {
		return memory.Record{}, memorystore.ErrInvalidCommand
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO memory_records(memory_id, vault_id, kind, state, scope_kind, workspace_root_hash, workspace_id, sensitivity, confidence, revision, created_at, updated_at, reviewed_at, expires_at, superseded_by_memory_id, created_event_id, last_event_id)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, payload.MemoryID, input.VaultID, string(payload.Kind), string(payload.State), string(payload.Scope.Kind), workspaceHash, workspaceID, string(payload.Sensitivity), payload.Confidence, payload.Revision, payload.CreatedAt, payload.UpdatedAt, payload.ReviewedAt, payload.ExpiresAt, payload.SupersededBy, eventRecord.ID, eventRecord.ID); err != nil {
		return memory.Record{}, fmt.Errorf("insert memory pointer: %w", err)
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, eventRecord.ID, requestHash); err != nil {
		return memory.Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return memory.Record{}, fmt.Errorf("commit memory candidate: %w", err)
	}
	return memoryFromEvent(eventRecord)
}

func (s *Store) TransitionMemory(ctx context.Context, input memorystore.TransitionInput) (memory.Record, error) {
	if input.VaultID == "" || input.MemoryID == "" || input.ExpectedRevision < 1 || input.NextState.Validate() != nil || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 1024 || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 || (input.NextState == memory.StateSuperseded) != (strings.TrimSpace(input.SupersededBy) != "") || input.SupersededBy == input.MemoryID || len(input.SupersededBy) > 128 {
		return memory.Record{}, memorystore.ErrInvalidCommand
	}
	requestHash, err := memoryCommandHash(input)
	if err != nil {
		return memory.Record{}, fmt.Errorf("hash memory transition command: %w", err)
	}
	occurredAt := input.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = s.now()
	}
	occurredAt = occurredAt.UTC()
	if !input.ExpiresAt.IsZero() {
		input.ExpiresAt = input.ExpiresAt.UTC()
		if !input.ExpiresAt.After(occurredAt) || (input.NextState != memory.StateApproved && input.NextState != memory.StateStable) {
			return memory.Record{}, memorystore.ErrInvalidCommand
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return memory.Record{}, fmt.Errorf("begin memory transition transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existing, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, requestHash)
	if err != nil {
		return memory.Record{}, mapMemoryIdempotencyError(err)
	}
	if found {
		if existing.Type != memoryStateChangedEventType {
			return memory.Record{}, memorystore.ErrIdempotencyConflict
		}
		return memoryFromEvent(existing)
	}
	if err := requireActiveVault(ctx, tx, input.VaultID); err != nil {
		return memory.Record{}, err
	}
	pointer, err := scanMemoryPointer(tx.QueryRowContext(ctx, memorySelect+" WHERE memory_id = ? AND vault_id = ?", input.MemoryID, input.VaultID))
	if err != nil {
		return memory.Record{}, err
	}
	if pointer.revision != input.ExpectedRevision {
		return memory.Record{}, memorystore.ErrRevisionConflict
	}
	currentEvent, err := s.getEventInTx(tx, pointer.lastEventID)
	if err != nil {
		return memory.Record{}, err
	}
	current, err := memoryFromEvent(currentEvent)
	if err != nil {
		return memory.Record{}, err
	}
	current, err = normalizeMemoryScope(current, pointer)
	if err != nil {
		return memory.Record{}, err
	}
	if !memory.CanTransition(current.State, input.NextState) {
		return memory.Record{}, memorystore.ErrTransitionRejected
	}
	if occurredAt.Before(current.UpdatedAt) {
		return memory.Record{}, memorystore.ErrInvalidCommand
	}
	if input.NextState == memory.StateSuperseded {
		replacement, err := scanMemoryPointer(tx.QueryRowContext(ctx, memorySelect+" WHERE memory_id = ? AND vault_id = ?", input.SupersededBy, input.VaultID))
		if err != nil {
			return memory.Record{}, err
		}
		replacementExpired := false
		if replacement.expiresAt != "" {
			replacementExpiry, parseErr := time.Parse(time.RFC3339Nano, replacement.expiresAt)
			if parseErr != nil {
				return memory.Record{}, memorystore.ErrInvalidCommand
			}
			replacementExpired = !replacementExpiry.After(occurredAt)
		}
		if replacement.state != string(memory.StateApproved) && replacement.state != string(memory.StateStable) || replacement.scopeKind != pointer.scopeKind || replacement.workspaceID != pointer.workspaceID || replacement.workspaceRootHash != pointer.workspaceRootHash || replacementExpired {
			return memory.Record{}, memorystore.ErrTransitionRejected
		}
		current.SupersededBy = input.SupersededBy
	}
	if input.NextState == memory.StateApproved {
		current.ExpiresAt = input.ExpiresAt
	} else if !input.ExpiresAt.IsZero() {
		current.ExpiresAt = input.ExpiresAt
	}
	current.State = input.NextState
	current.Revision++
	current.UpdatedAt = occurredAt
	if current.ReviewedAt.IsZero() {
		current.ReviewedAt = occurredAt
	}
	payload := memoryPayloadFromRecord(current)
	payload.TransitionReason = strings.TrimSpace(input.Reason)
	eventRecord, err := s.memoryEvent(input.VaultID, memoryStateChangedEventType, payload, occurredAt)
	if err != nil {
		return memory.Record{}, err
	}
	if err := s.insertEvent(ctx, tx, eventRecord); err != nil {
		return memory.Record{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE memory_records SET state = ?, revision = ?, updated_at = ?, reviewed_at = ?, expires_at = ?, superseded_by_memory_id = ?, last_event_id = ? WHERE memory_id = ? AND vault_id = ? AND revision = ?`, string(current.State), current.Revision, payload.UpdatedAt, payload.ReviewedAt, payload.ExpiresAt, payload.SupersededBy, eventRecord.ID, input.MemoryID, input.VaultID, input.ExpectedRevision)
	if err != nil {
		return memory.Record{}, fmt.Errorf("update memory pointer: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return memory.Record{}, fmt.Errorf("read memory transition result: %w", err)
	}
	if rows != 1 {
		return memory.Record{}, memorystore.ErrRevisionConflict
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, eventRecord.ID, requestHash); err != nil {
		return memory.Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return memory.Record{}, fmt.Errorf("commit memory transition: %w", err)
	}
	return memoryFromEvent(eventRecord)
}

func (s *Store) GetMemory(ctx context.Context, vaultID, memoryID string) (memory.Record, error) {
	if vaultID == "" || memoryID == "" {
		return memory.Record{}, memorystore.ErrInvalidCommand
	}
	pointer, err := scanMemoryPointer(s.db.QueryRowContext(ctx, memorySelect+" WHERE memory_id = ? AND vault_id = ?", memoryID, vaultID))
	if err != nil {
		return memory.Record{}, err
	}
	record, err := s.Get(ctx, pointer.lastEventID)
	if err != nil {
		return memory.Record{}, err
	}
	result, err := memoryFromEvent(record)
	if err != nil {
		return memory.Record{}, err
	}
	return normalizeMemoryScope(result, pointer)
}

func (s *Store) ListMemoryCandidates(ctx context.Context, vaultID string, limit int) ([]memory.Record, error) {
	if vaultID == "" || limit < 1 || limit > 200 {
		return nil, memorystore.ErrInvalidCommand
	}
	return s.listMemories(ctx, memorySelect+" WHERE vault_id = ? AND state = ? ORDER BY created_at, memory_id LIMIT ?", vaultID, string(memory.StateCandidate), limit)
}

func (s *Store) ListActiveMemories(ctx context.Context, input memorystore.ListActiveInput) ([]memory.Record, error) {
	if input.VaultID == "" || !workspacemapping.ValidWorkspaceID(input.WorkspaceID) || input.Limit < 1 || input.Limit > 200 {
		return nil, memorystore.ErrInvalidCommand
	}
	at := input.At.UTC()
	if at.IsZero() {
		at = s.now().UTC()
	}
	return s.listMemories(ctx, memorySelect+` WHERE vault_id = ? AND state IN (?, ?) AND (expires_at = '' OR julianday(expires_at) > julianday(?)) AND (scope_kind = ? OR (scope_kind = ? AND workspace_id = ?))
		ORDER BY CASE state WHEN 'stable' THEN 0 ELSE 1 END, confidence DESC, updated_at DESC, memory_id LIMIT ?`, input.VaultID, string(memory.StateApproved), string(memory.StateStable), at.Format(time.RFC3339Nano), string(memory.ScopeVault), string(memory.ScopeWorkspace), input.WorkspaceID, input.Limit)
}

func (s *Store) ListMemories(ctx context.Context, input memorystore.ListInput) ([]memory.Record, error) {
	if input.VaultID == "" || input.Limit < 1 || input.Limit > 200 {
		return nil, memorystore.ErrInvalidCommand
	}
	if !input.ExpiredAt.IsZero() {
		return s.listMemories(ctx, memorySelect+` WHERE vault_id = ? AND state IN (?, ?) AND expires_at != '' AND julianday(expires_at) <= julianday(?) ORDER BY julianday(expires_at), memory_id LIMIT ?`, input.VaultID, string(memory.StateApproved), string(memory.StateStable), input.ExpiredAt.UTC().Format(time.RFC3339Nano), input.Limit)
	}
	return s.listMemories(ctx, memorySelect+" WHERE vault_id = ? ORDER BY updated_at DESC, memory_id LIMIT ?", input.VaultID, input.Limit)
}

func (s *Store) listMemories(ctx context.Context, query string, arguments ...any) ([]memory.Record, error) {
	rows, err := s.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query memory pointers: %w", err)
	}
	var pointers []memoryPointer
	for rows.Next() {
		pointer, err := scanMemoryPointer(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		pointers = append(pointers, pointer)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate memory pointers: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close memory pointers: %w", err)
	}
	result := make([]memory.Record, 0, len(pointers))
	if len(pointers) == 0 {
		return result, nil
	}
	ids := make([]any, 0, len(pointers))
	for _, pointer := range pointers {
		ids = append(ids, pointer.lastEventID)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	eventRows, err := s.db.QueryContext(ctx, `SELECT event_id, vault_id, event_type, schema_version, sensitivity, payload_envelope, occurred_at FROM events WHERE event_id IN (`+placeholders+`)`, ids...)
	if err != nil {
		return nil, fmt.Errorf("query memory events: %w", err)
	}
	defer eventRows.Close()
	events := make(map[string]event.Record, len(pointers))
	for eventRows.Next() {
		record, err := s.scanRecord(eventRows)
		if err != nil {
			return nil, err
		}
		events[record.ID] = record
	}
	if err := eventRows.Err(); err != nil {
		return nil, err
	}
	for _, pointer := range pointers {
		eventRecord, ok := events[pointer.lastEventID]
		if !ok {
			return nil, ErrNotFound
		}
		record, err := memoryFromEvent(eventRecord)
		if err != nil {
			return nil, err
		}
		record, err = normalizeMemoryScope(record, pointer)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}

func requireMemoryEvidence(ctx context.Context, tx *sql.Tx, vaultID string, evidenceIDs []string) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(evidenceIDs)), ",")
	arguments := make([]any, 0, len(evidenceIDs)+1)
	arguments = append(arguments, vaultID)
	for _, evidenceID := range evidenceIDs {
		arguments = append(arguments, evidenceID)
	}
	var count int
	query := "SELECT COUNT(DISTINCT event_id) FROM events WHERE vault_id = ? AND event_id IN (" + placeholders + ")"
	if err := tx.QueryRowContext(ctx, query, arguments...).Scan(&count); err != nil {
		return fmt.Errorf("verify memory evidence: %w", err)
	}
	if count != len(evidenceIDs) {
		return memorystore.ErrEvidenceNotFound
	}
	return nil
}

func (s *Store) memoryEvent(vaultID, eventType string, payload memoryPayload, occurredAt time.Time) (event.Record, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return event.Record{}, fmt.Errorf("encode memory event: %w", err)
	}
	return s.newEventRecord(vaultID, eventType, memoryEventSchemaVersion, payload.Sensitivity, encoded, occurredAt)
}

func memoryPayloadFromRecord(record memory.Record) memoryPayload {
	reviewedAt := ""
	if !record.ReviewedAt.IsZero() {
		reviewedAt = record.ReviewedAt.Format(time.RFC3339Nano)
	}
	expiresAt := ""
	if !record.ExpiresAt.IsZero() {
		expiresAt = record.ExpiresAt.Format(time.RFC3339Nano)
	}
	return memoryPayload{
		MemoryID: record.ID, Kind: record.Kind, State: record.State, Scope: record.Scope,
		Statement: record.Statement, Rationale: record.Rationale, Applicability: record.Applicability,
		EvidenceEventIDs: append([]string(nil), record.EvidenceEventIDs...), SourceActor: record.SourceActor,
		Confidence: record.Confidence, Sensitivity: record.Sensitivity, Revision: record.Revision,
		CreatedAt: record.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: record.UpdatedAt.Format(time.RFC3339Nano),
		ReviewedAt: reviewedAt, ExpiresAt: expiresAt, SupersededBy: record.SupersededBy, CreatedEventID: record.CreatedEventID,
	}
}

func memoryFromEvent(record event.Record) (memory.Record, error) {
	if record.Type != memoryCandidateCreatedEventType && record.Type != memoryStateChangedEventType && record.Type != memorySnapshotEventType || record.SchemaVersion != memoryLegacyEventSchemaVersion && record.SchemaVersion != memoryEventSchemaVersion {
		return memory.Record{}, memorystore.ErrNotFound
	}
	var payload memoryPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return memory.Record{}, fmt.Errorf("decode memory event: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
	if err != nil {
		return memory.Record{}, memorystore.ErrInvalidCommand
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, payload.UpdatedAt)
	if err != nil {
		return memory.Record{}, memorystore.ErrInvalidCommand
	}
	reviewedAt := time.Time{}
	if payload.ReviewedAt != "" {
		reviewedAt, err = time.Parse(time.RFC3339Nano, payload.ReviewedAt)
		if err != nil {
			return memory.Record{}, memorystore.ErrInvalidCommand
		}
	}
	expiresAt := time.Time{}
	if payload.ExpiresAt != "" {
		expiresAt, err = time.Parse(time.RFC3339Nano, payload.ExpiresAt)
		if err != nil {
			return memory.Record{}, memorystore.ErrInvalidCommand
		}
	}
	if validateMemoryScopeForSchema(record.VaultID, record.SchemaVersion, payload.Scope) != nil {
		return memory.Record{}, memorystore.ErrInvalidCommand
	}
	createdEventID := payload.CreatedEventID
	if createdEventID == "" && (record.Type == memoryCandidateCreatedEventType || record.Type == memorySnapshotEventType && payload.Revision == 1) {
		createdEventID = record.ID
	}
	result := memory.Record{
		ID: payload.MemoryID, VaultID: record.VaultID, Kind: payload.Kind, State: payload.State, Scope: payload.Scope,
		Statement: payload.Statement, Rationale: payload.Rationale, Applicability: payload.Applicability,
		EvidenceEventIDs: payload.EvidenceEventIDs, SourceActor: payload.SourceActor, Confidence: payload.Confidence,
		Sensitivity: payload.Sensitivity, Revision: payload.Revision, CreatedAt: createdAt, UpdatedAt: updatedAt,
		ReviewedAt: reviewedAt, ExpiresAt: expiresAt, SupersededBy: payload.SupersededBy, CreatedEventID: createdEventID, LastEventID: record.ID,
	}
	if result.Sensitivity != record.Sensitivity || result.Validate() != nil {
		return memory.Record{}, memorystore.ErrInvalidCommand
	}
	return result, nil
}

func validateMemoryScopeForSchema(vaultID string, schemaVersion int, scope memory.Scope) error {
	if scope.Validate() != nil || strings.TrimSpace(vaultID) == "" {
		return memorystore.ErrInvalidCommand
	}
	if scope.Kind == memory.ScopeVault {
		return nil
	}
	switch schemaVersion {
	case memoryLegacyEventSchemaVersion:
		if scope.WorkspaceID != "" || scope.SourceWorkspaceHash != "" || scope.WorkspaceRoot == "" {
			return memorystore.ErrInvalidCommand
		}
	case memoryEventSchemaVersion:
		if scope.WorkspaceRoot != "" || !workspacemapping.ValidWorkspaceID(scope.WorkspaceID) || scope.WorkspaceID != workspacemapping.ID(vaultID, scope.SourceWorkspaceHash) {
			return memorystore.ErrInvalidCommand
		}
	default:
		return memorystore.ErrInvalidCommand
	}
	return nil
}

func memoryScopeIdentity(vaultID string, scope memory.Scope) (string, string, error) {
	if scope.Validate() != nil {
		return "", "", memorystore.ErrInvalidCommand
	}
	if scope.Kind == memory.ScopeVault {
		return "", "", nil
	}
	if scope.WorkspaceID != "" {
		if scope.WorkspaceID != workspacemapping.ID(vaultID, scope.SourceWorkspaceHash) {
			return "", "", memorystore.ErrInvalidCommand
		}
		return scope.SourceWorkspaceHash, scope.WorkspaceID, nil
	}
	sourceHash := workspaceRootHash(scope.WorkspaceRoot)
	return sourceHash, workspacemapping.ID(vaultID, sourceHash), nil
}

func normalizeMemoryScope(record memory.Record, pointer memoryPointer) (memory.Record, error) {
	if record.ID != pointer.memoryID || record.VaultID != pointer.vaultID || string(record.Scope.Kind) != pointer.scopeKind {
		return memory.Record{}, memorystore.ErrInvalidCommand
	}
	sourceHash, workspaceID, err := memoryScopeIdentity(record.VaultID, record.Scope)
	if err != nil || sourceHash != pointer.workspaceRootHash || workspaceID != pointer.workspaceID {
		return memory.Record{}, memorystore.ErrInvalidCommand
	}
	if record.Scope.Kind == memory.ScopeWorkspace {
		record.Scope = memory.Scope{Kind: memory.ScopeWorkspace, WorkspaceID: workspaceID, SourceWorkspaceHash: sourceHash}
	}
	if record.Validate() != nil {
		return memory.Record{}, memorystore.ErrInvalidCommand
	}
	return record, nil
}

const memorySelect = `SELECT memory_id, vault_id, kind, state, scope_kind, workspace_root_hash, workspace_id, sensitivity, confidence, revision, created_at, updated_at, reviewed_at, expires_at, superseded_by_memory_id, created_event_id, last_event_id FROM memory_records`

func scanMemoryPointer(row scanner) (memoryPointer, error) {
	var pointer memoryPointer
	if err := row.Scan(&pointer.memoryID, &pointer.vaultID, &pointer.kind, &pointer.state, &pointer.scopeKind, &pointer.workspaceRootHash, &pointer.workspaceID, &pointer.sensitivity, &pointer.confidence, &pointer.revision, &pointer.createdAt, &pointer.updatedAt, &pointer.reviewedAt, &pointer.expiresAt, &pointer.supersededBy, &pointer.createdEventID, &pointer.lastEventID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return memoryPointer{}, memorystore.ErrNotFound
		}
		return memoryPointer{}, fmt.Errorf("scan memory pointer: %w", err)
	}
	return pointer, nil
}

func memoryCommandHash(input any) ([]byte, error) {
	return requestHash(input)
}

func mapMemoryIdempotencyError(err error) error {
	switch {
	case errors.Is(err, ErrIdempotencyConflict):
		return memorystore.ErrIdempotencyConflict
	case errors.Is(err, ErrIdempotencyUnverifiable):
		return memorystore.ErrIdempotencyUnverified
	default:
		return err
	}
}
