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
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/id"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
)

const (
	taskContractCreatedEventType   = "task.contract.created"
	taskContractRevisedEventType   = "task.contract.revised"
	taskContractEventSchemaVersion = 1
)

type taskContractPayload struct {
	TaskID               string                           `json:"task_id"`
	Revision             int                              `json:"revision"`
	WorkspaceRoot        string                           `json:"workspace_root"`
	BaselineCommit       string                           `json:"baseline_commit"`
	Goal                 string                           `json:"goal"`
	AllowedPaths         []string                         `json:"allowed_paths"`
	ForbiddenActions     []string                         `json:"forbidden_actions"`
	AcceptanceCriteria   []string                         `json:"acceptance_criteria"`
	VerificationCommands []taskVerificationCommandPayload `json:"verification_commands,omitempty"`
	Risk                 task.Risk                        `json:"risk"`
	CreatedAt            string                           `json:"created_at"`
}

type taskVerificationCommandPayload struct {
	RuleID           string   `json:"rule_id"`
	Arguments        []string `json:"arguments"`
	WorkingDirectory string   `json:"working_directory"`
}

func (s *Store) CreateTaskContract(ctx context.Context, input taskstore.CreateInput) (taskstore.Created, error) {
	occurredAt := input.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = s.now()
	}
	occurredAt = occurredAt.UTC()
	validation := task.ContractRevision{
		TaskID: "validation-task", Revision: 1, BaselineCommit: input.BaselineCommit, Goal: input.Goal,
		AllowedPaths: input.AllowedPaths, ForbiddenActions: input.ForbiddenActions,
		AcceptanceCriteria: input.AcceptanceCriteria, VerificationCommands: input.VerificationCommands, Risk: input.Risk, CreatedAt: occurredAt, EventID: "validation-event",
	}
	if input.VaultID == "" || input.WorkspaceRoot == "" || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 || validation.Validate() != nil {
		return taskstore.Created{}, taskstore.ErrInvalidCommand
	}
	requestHash, err := taskCommandHash(input)
	if err != nil {
		return taskstore.Created{}, fmt.Errorf("hash task contract command: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return taskstore.Created{}, fmt.Errorf("begin task contract transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	existingEvent, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, requestHash)
	if err != nil {
		return taskstore.Created{}, mapTaskIdempotencyError(err)
	}
	if found {
		return s.taskResultFromEvent(ctx, tx, existingEvent)
	}
	if err := requireActiveVault(ctx, tx, input.VaultID); err != nil {
		return taskstore.Created{}, err
	}

	taskID, err := id.UUIDv7(occurredAt, s.random)
	if err != nil {
		return taskstore.Created{}, fmt.Errorf("generate task id: %w", err)
	}
	payload := taskContractPayload{
		TaskID: taskID, Revision: 1, WorkspaceRoot: input.WorkspaceRoot, BaselineCommit: input.BaselineCommit,
		Goal: input.Goal, AllowedPaths: append([]string(nil), input.AllowedPaths...), ForbiddenActions: append([]string(nil), input.ForbiddenActions...),
		AcceptanceCriteria: append([]string(nil), input.AcceptanceCriteria...), VerificationCommands: verificationCommandsToPayload(input.VerificationCommands), Risk: input.Risk, CreatedAt: occurredAt.Format(time.RFC3339Nano),
	}
	eventRecord, err := s.taskEvent(input.VaultID, payload, occurredAt)
	if err != nil {
		return taskstore.Created{}, err
	}
	if err := s.insertEvent(ctx, tx, eventRecord); err != nil {
		return taskstore.Created{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO tasks(task_id, vault_id, workspace_root_hash, baseline_commit, status, current_revision, created_at, updated_at, last_event_id)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`, taskID, input.VaultID, workspaceRootHash(input.WorkspaceRoot), input.BaselineCommit, string(task.StatusContracted), 1, payload.CreatedAt, payload.CreatedAt, eventRecord.ID); err != nil {
		return taskstore.Created{}, fmt.Errorf("insert task: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_contract_revisions(task_id, revision, baseline_commit, created_at, event_id)
		VALUES(?, ?, ?, ?, ?)`, taskID, 1, input.BaselineCommit, payload.CreatedAt, eventRecord.ID); err != nil {
		return taskstore.Created{}, fmt.Errorf("insert task contract revision: %w", err)
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, eventRecord.ID, requestHash); err != nil {
		return taskstore.Created{}, err
	}
	if err := tx.Commit(); err != nil {
		return taskstore.Created{}, fmt.Errorf("commit task contract: %w", err)
	}
	return taskCreatedFromPayload(input.VaultID, payload, eventRecord.ID)
}

func (s *Store) ReviseTaskContract(ctx context.Context, input taskstore.ReviseInput) (taskstore.Created, error) {
	if input.VaultID == "" || input.TaskID == "" || input.ExpectedRevision < 1 || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 {
		return taskstore.Created{}, taskstore.ErrInvalidCommand
	}
	occurredAt := input.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = s.now()
	}
	occurredAt = occurredAt.UTC()
	requestHash, err := taskRevisionCommandHash(input)
	if err != nil {
		return taskstore.Created{}, fmt.Errorf("hash task revision command: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return taskstore.Created{}, fmt.Errorf("begin task revision transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existingEvent, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, requestHash)
	if err != nil {
		return taskstore.Created{}, mapTaskIdempotencyError(err)
	}
	if found {
		return s.taskResultFromEvent(ctx, tx, existingEvent)
	}
	if err := requireActiveVault(ctx, tx, input.VaultID); err != nil {
		return taskstore.Created{}, err
	}
	pointer, err := scanTaskPointer(tx.QueryRowContext(ctx, taskSelect+" WHERE task_id = ? AND vault_id = ?", input.TaskID, input.VaultID))
	if err != nil {
		return taskstore.Created{}, err
	}
	if pointer.currentRevision != input.ExpectedRevision {
		return taskstore.Created{}, taskstore.ErrRevisionConflict
	}
	if task.Status(pointer.status) != task.StatusContracted {
		return taskstore.Created{}, taskstore.ErrRevisionConflict
	}
	currentEvent, err := s.getEventInTx(tx, pointer.lastEventID)
	if err != nil {
		return taskstore.Created{}, err
	}
	current, err := taskFromEvent(currentEvent, pointer)
	if err != nil {
		return taskstore.Created{}, err
	}
	nextRevision := input.ExpectedRevision + 1
	validation := task.ContractRevision{
		TaskID: input.TaskID, Revision: nextRevision, BaselineCommit: current.BaselineCommit,
		Goal: input.Goal, AllowedPaths: input.AllowedPaths, ForbiddenActions: input.ForbiddenActions,
		AcceptanceCriteria: input.AcceptanceCriteria, VerificationCommands: input.VerificationCommands, Risk: input.Risk, CreatedAt: occurredAt, EventID: "validation-event",
	}
	if validation.Validate() != nil {
		return taskstore.Created{}, taskstore.ErrInvalidCommand
	}
	payload := taskContractPayload{
		TaskID: input.TaskID, Revision: nextRevision, WorkspaceRoot: current.WorkspaceRoot, BaselineCommit: current.BaselineCommit,
		Goal: input.Goal, AllowedPaths: append([]string(nil), input.AllowedPaths...), ForbiddenActions: append([]string(nil), input.ForbiddenActions...),
		AcceptanceCriteria: append([]string(nil), input.AcceptanceCriteria...), VerificationCommands: verificationCommandsToPayload(input.VerificationCommands), Risk: input.Risk, CreatedAt: occurredAt.Format(time.RFC3339Nano),
	}
	eventRecord, err := s.taskEventOfType(input.VaultID, taskContractRevisedEventType, payload, occurredAt)
	if err != nil {
		return taskstore.Created{}, err
	}
	if err := s.insertEvent(ctx, tx, eventRecord); err != nil {
		return taskstore.Created{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_contract_revisions(task_id, revision, baseline_commit, created_at, event_id) VALUES(?, ?, ?, ?, ?)`, input.TaskID, nextRevision, current.BaselineCommit, payload.CreatedAt, eventRecord.ID); err != nil {
		return taskstore.Created{}, fmt.Errorf("insert task contract revision: %w", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE tasks SET current_revision = ?, updated_at = ?, last_event_id = ? WHERE task_id = ? AND vault_id = ? AND current_revision = ?`, nextRevision, payload.CreatedAt, eventRecord.ID, input.TaskID, input.VaultID, input.ExpectedRevision)
	if err != nil {
		return taskstore.Created{}, fmt.Errorf("update task revision: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return taskstore.Created{}, fmt.Errorf("read task revision result: %w", err)
	}
	if rows != 1 {
		return taskstore.Created{}, taskstore.ErrRevisionConflict
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, eventRecord.ID, requestHash); err != nil {
		return taskstore.Created{}, err
	}
	if err := tx.Commit(); err != nil {
		return taskstore.Created{}, fmt.Errorf("commit task revision: %w", err)
	}
	return taskResultFromPayload(input.VaultID, payload, eventRecord.ID, current.CreatedAt)
}

func (s *Store) GetTask(ctx context.Context, taskID string) (task.Record, error) {
	if taskID == "" {
		return task.Record{}, taskstore.ErrInvalidCommand
	}
	pointer, err := scanTaskPointer(s.db.QueryRowContext(ctx, taskSelect+" WHERE task_id = ?", taskID))
	if err != nil {
		return task.Record{}, err
	}
	record, err := s.Get(ctx, pointer.lastEventID)
	if err != nil {
		return task.Record{}, err
	}
	return taskFromEvent(record, pointer)
}

func (s *Store) GetTaskContract(ctx context.Context, taskID string, revision int) (task.ContractRevision, error) {
	if taskID == "" || revision < 1 {
		return task.ContractRevision{}, taskstore.ErrInvalidCommand
	}
	var storedTaskID, baselineCommit, createdAt, eventID string
	var storedRevision int
	if err := s.db.QueryRowContext(ctx, taskContractSelect+" WHERE task_id = ? AND revision = ?", taskID, revision).Scan(&storedTaskID, &storedRevision, &baselineCommit, &createdAt, &eventID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return task.ContractRevision{}, taskstore.ErrNotFound
		}
		return task.ContractRevision{}, fmt.Errorf("scan task contract pointer: %w", err)
	}
	record, err := s.Get(ctx, eventID)
	if err != nil {
		return task.ContractRevision{}, err
	}
	return contractFromEvent(record, storedTaskID, storedRevision, baselineCommit, createdAt)
}

func (s *Store) taskResultFromEvent(ctx context.Context, tx *sql.Tx, record event.Record) (taskstore.Created, error) {
	if record.Type != taskContractCreatedEventType && record.Type != taskContractRevisedEventType {
		return taskstore.Created{}, taskstore.ErrIdempotencyConflict
	}
	var payload taskContractPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return taskstore.Created{}, fmt.Errorf("decode task contract event: %w", err)
	}
	var pointerEventID, taskCreatedAt string
	if err := tx.QueryRowContext(ctx, `SELECT event_id FROM task_contract_revisions WHERE task_id = ? AND revision = ?`, payload.TaskID, payload.Revision).Scan(&pointerEventID); err != nil {
		return taskstore.Created{}, fmt.Errorf("read replayed contract pointer: %w", err)
	}
	if pointerEventID != record.ID {
		return taskstore.Created{}, errors.New("replayed contract pointer disagrees with event")
	}
	if err := tx.QueryRowContext(ctx, `SELECT created_at FROM tasks WHERE task_id = ?`, payload.TaskID).Scan(&taskCreatedAt); err != nil {
		return taskstore.Created{}, fmt.Errorf("read replayed task creation time: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, taskCreatedAt)
	if err != nil {
		return taskstore.Created{}, fmt.Errorf("parse replayed task creation time: %w", err)
	}
	return taskResultFromPayload(record.VaultID, payload, record.ID, createdAt)
}

func (s *Store) taskEvent(vaultID string, payload taskContractPayload, occurredAt time.Time) (event.Record, error) {
	return s.taskEventOfType(vaultID, taskContractCreatedEventType, payload, occurredAt)
}

func (s *Store) taskEventOfType(vaultID, eventType string, payload taskContractPayload, occurredAt time.Time) (event.Record, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return event.Record{}, fmt.Errorf("encode task contract event: %w", err)
	}
	return s.newEventRecord(vaultID, eventType, taskContractEventSchemaVersion, event.SensitivityPrivate, encoded, occurredAt)
}

func taskCreatedFromPayload(vaultID string, payload taskContractPayload, eventID string) (taskstore.Created, error) {
	createdAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
	if err != nil {
		return taskstore.Created{}, err
	}
	return taskResultFromPayload(vaultID, payload, eventID, createdAt)
}

func taskResultFromPayload(vaultID string, payload taskContractPayload, eventID string, taskCreatedAt time.Time) (taskstore.Created, error) {
	revisionAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
	if err != nil {
		return taskstore.Created{}, err
	}
	result := taskstore.Created{
		Task:     task.Record{ID: payload.TaskID, VaultID: vaultID, WorkspaceRoot: payload.WorkspaceRoot, BaselineCommit: payload.BaselineCommit, Status: task.StatusContracted, CurrentRevision: payload.Revision, CreatedAt: taskCreatedAt, UpdatedAt: revisionAt, LastEventID: eventID},
		Contract: task.ContractRevision{TaskID: payload.TaskID, Revision: payload.Revision, BaselineCommit: payload.BaselineCommit, Goal: payload.Goal, AllowedPaths: payload.AllowedPaths, ForbiddenActions: payload.ForbiddenActions, AcceptanceCriteria: payload.AcceptanceCriteria, VerificationCommands: verificationCommandsFromPayload(payload.VerificationCommands), Risk: payload.Risk, CreatedAt: revisionAt, EventID: eventID},
	}
	if err := result.Task.Validate(); err != nil {
		return taskstore.Created{}, err
	}
	if err := result.Contract.Validate(); err != nil {
		return taskstore.Created{}, err
	}
	return result, nil
}

const taskSelect = `SELECT task_id, vault_id, workspace_root_hash, baseline_commit,
	COALESCE((SELECT status FROM task_outcomes WHERE task_outcomes.task_id = tasks.task_id), status),
	current_revision, created_at, updated_at, last_event_id FROM tasks`
const taskContractSelect = `SELECT task_id, revision, baseline_commit, created_at, event_id FROM task_contract_revisions`

type taskPointer struct {
	taskID, vaultID, workspaceRootHash, baselineCommit, status, createdAt, updatedAt, lastEventID string
	currentRevision                                                                               int
}

func scanTaskPointer(row scanner) (taskPointer, error) {
	var result taskPointer
	if err := row.Scan(&result.taskID, &result.vaultID, &result.workspaceRootHash, &result.baselineCommit, &result.status, &result.currentRevision, &result.createdAt, &result.updatedAt, &result.lastEventID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return taskPointer{}, taskstore.ErrNotFound
		}
		return taskPointer{}, fmt.Errorf("scan task pointer: %w", err)
	}
	return result, nil
}

func taskFromEvent(record event.Record, pointer taskPointer) (task.Record, error) {
	if record.Type != taskContractCreatedEventType && record.Type != taskContractRevisedEventType || record.VaultID != pointer.vaultID {
		return task.Record{}, errors.New("stored task event type or Vault is invalid")
	}
	var payload taskContractPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return task.Record{}, fmt.Errorf("decode task event: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, pointer.createdAt)
	if err != nil {
		return task.Record{}, fmt.Errorf("parse task created_at: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, pointer.updatedAt)
	if err != nil {
		return task.Record{}, fmt.Errorf("parse task updated_at: %w", err)
	}
	expectedEventTime := pointer.updatedAt
	if record.Type == taskContractCreatedEventType {
		expectedEventTime = pointer.createdAt
	}
	if payload.TaskID != pointer.taskID || payload.BaselineCommit != pointer.baselineCommit || payload.Revision != pointer.currentRevision || payload.CreatedAt != expectedEventTime || workspaceRootHash(payload.WorkspaceRoot) != pointer.workspaceRootHash || record.ID != pointer.lastEventID {
		return task.Record{}, errors.New("task event and pointer disagree")
	}
	result := task.Record{ID: pointer.taskID, VaultID: pointer.vaultID, WorkspaceRoot: payload.WorkspaceRoot, BaselineCommit: pointer.baselineCommit, Status: task.Status(pointer.status), CurrentRevision: pointer.currentRevision, CreatedAt: createdAt, UpdatedAt: updatedAt, LastEventID: pointer.lastEventID}
	if err := result.Validate(); err != nil {
		return task.Record{}, fmt.Errorf("validate stored task: %w", err)
	}
	return result, nil
}

func workspaceRootHash(root string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(root))) }

func contractFromEvent(record event.Record, taskID string, revision int, baselineCommit, createdAt string) (task.ContractRevision, error) {
	if record.Type != taskContractCreatedEventType && record.Type != taskContractRevisedEventType {
		return task.ContractRevision{}, errors.New("stored task contract event type is invalid")
	}
	var payload taskContractPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return task.ContractRevision{}, fmt.Errorf("decode task contract event: %w", err)
	}
	parsedCreatedAt, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return task.ContractRevision{}, fmt.Errorf("parse contract created_at: %w", err)
	}
	if payload.TaskID != taskID || payload.Revision != revision || payload.BaselineCommit != baselineCommit || payload.CreatedAt != createdAt {
		return task.ContractRevision{}, errors.New("task contract event and pointer disagree")
	}
	result := task.ContractRevision{
		TaskID: taskID, Revision: revision, BaselineCommit: baselineCommit, Goal: payload.Goal,
		AllowedPaths: payload.AllowedPaths, ForbiddenActions: payload.ForbiddenActions,
		AcceptanceCriteria: payload.AcceptanceCriteria, VerificationCommands: verificationCommandsFromPayload(payload.VerificationCommands), Risk: payload.Risk,
		CreatedAt: parsedCreatedAt, EventID: record.ID,
	}
	if err := result.Validate(); err != nil {
		return task.ContractRevision{}, fmt.Errorf("validate stored task contract: %w", err)
	}
	return result, nil
}

func verificationCommandsToPayload(commands []task.VerificationCommand) []taskVerificationCommandPayload {
	cloned := make([]taskVerificationCommandPayload, len(commands))
	for index, command := range commands {
		cloned[index] = taskVerificationCommandPayload{RuleID: command.RuleID, Arguments: append([]string(nil), command.Arguments...), WorkingDirectory: command.WorkingDirectory}
	}
	return cloned
}

func verificationCommandsFromPayload(commands []taskVerificationCommandPayload) []task.VerificationCommand {
	cloned := make([]task.VerificationCommand, len(commands))
	for index, command := range commands {
		cloned[index] = task.VerificationCommand{RuleID: command.RuleID, Arguments: append([]string(nil), command.Arguments...), WorkingDirectory: command.WorkingDirectory}
	}
	return cloned
}

func (s *Store) getEventInTx(tx *sql.Tx, eventID string) (event.Record, error) {
	return s.scanRecord(tx.QueryRow(`SELECT event_id, vault_id, event_type, schema_version, sensitivity, payload_envelope, occurred_at FROM events WHERE event_id = ?`, eventID))
}

func requireActiveVault(ctx context.Context, tx *sql.Tx, vaultID string) error {
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM vault_states WHERE vault_id = ?`, vaultID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return taskstore.ErrInvalidCommand
		}
		return fmt.Errorf("read task Vault: %w", err)
	}
	if status != "active" {
		return taskstore.ErrInvalidCommand
	}
	return nil
}

func taskCommandHash(input taskstore.CreateInput) ([]byte, error) {
	input.OccurredAt = input.OccurredAt.UTC()
	return requestHash(input)
}

func taskRevisionCommandHash(input taskstore.ReviseInput) ([]byte, error) {
	input.OccurredAt = input.OccurredAt.UTC()
	return requestHash(input)
}

func mapTaskIdempotencyError(err error) error {
	switch {
	case errors.Is(err, ErrIdempotencyConflict):
		return taskstore.ErrIdempotencyConflict
	case errors.Is(err, ErrIdempotencyUnverifiable):
		return taskstore.ErrIdempotencyUnverified
	default:
		return err
	}
}
