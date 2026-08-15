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
	"github.com/0disoft/zdp-desktop-talos/internal/domain/planning"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/id"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/modelstore"
)

const modelEgressEventSchemaVersion = 1

type modelEgressPayload struct {
	ReceiptID            string                `json:"receipt_id"`
	TaskID               string                `json:"task_id"`
	ContractRevision     int                   `json:"contract_revision"`
	ProviderKey          string                `json:"provider_key"`
	ModelKey             string                `json:"model_key"`
	RequestID            string                `json:"request_id"`
	PromptVersion        string                `json:"prompt_version"`
	ContextHash          string                `json:"context_hash"`
	RequestHash          string                `json:"request_hash"`
	ResponseHash         string                `json:"response_hash,omitempty"`
	ProviderCallID       string                `json:"provider_call_id,omitempty"`
	ContextItems         int                   `json:"context_items"`
	InputBytes           int                   `json:"input_bytes"`
	OutputBytes          int                   `json:"output_bytes"`
	RedactionCount       int                   `json:"redaction_count"`
	InputTokens          int                   `json:"input_tokens"`
	CachedInputTokens    int                   `json:"cached_input_tokens"`
	OutputTokens         int                   `json:"output_tokens"`
	ReservedInputTokens  int                   `json:"reserved_input_tokens,omitempty"`
	ReservedOutputTokens int                   `json:"reserved_output_tokens,omitempty"`
	Status               planning.EgressStatus `json:"status"`
	SafeErrorCode        string                `json:"safe_error_code,omitempty"`
	CreatedAt            string                `json:"created_at"`
	UpdatedAt            string                `json:"updated_at"`
}

func (s *Store) PrepareModelEgress(ctx context.Context, input modelstore.PrepareInput) (planning.EgressReceipt, error) {
	now := normalizedTime(input.OccurredAt, s.now)
	if input.VaultID == "" || input.TaskID == "" || input.ContractRevision < 1 || !planning.ValidKey(input.ProviderKey) || !planning.ValidKey(input.ModelKey) || !planning.ValidKey(input.PromptVersion) || input.RequestID == "" || len(input.RequestID) > 96 || !validSHA256(input.ContextHash) || !validSHA256(input.RequestHash) || input.ContextItems < 1 || input.ContextItems > 64 || input.InputBytes < 1 || input.InputBytes > 1<<20 || input.RedactionCount < 0 || input.RedactionCount > 10000 || input.ReservedInputTokens < 1 || input.ReservedOutputTokens < 256 || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 {
		return planning.EgressReceipt{}, modelstore.ErrInvalidCommand
	}
	requestHash, err := modelEgressCommandHash(input)
	if err != nil {
		return planning.EgressReceipt{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return planning.EgressReceipt{}, fmt.Errorf("begin model egress preparation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existing, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, requestHash)
	if err != nil {
		return planning.EgressReceipt{}, mapModelEgressIdempotencyError(err)
	}
	if found {
		return scanModelEgress(tx.QueryRowContext(ctx, modelEgressSelect+" WHERE created_event_id = ?", existing.ID))
	}
	if err := requireActiveVault(ctx, tx, input.VaultID); err != nil {
		return planning.EgressReceipt{}, err
	}
	var revision int
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT current_revision, status FROM tasks WHERE task_id = ? AND vault_id = ?`, input.TaskID, input.VaultID).Scan(&revision, &status); err != nil || revision != input.ContractRevision || task.Status(status) != task.StatusContracted {
		return planning.EgressReceipt{}, modelstore.ErrConflict
	}
	if err := reserveModelBudget(ctx, tx, input.VaultID, input.TaskID, now, input.Budget, input.ReservedInputTokens, input.ReservedOutputTokens); err != nil {
		if errors.Is(err, errTaskBudgetExceeded) {
			return planning.EgressReceipt{}, modelstore.ErrBudgetExceeded
		}
		return planning.EgressReceipt{}, err
	}
	receiptID, err := id.UUIDv7(now, s.random)
	if err != nil {
		return planning.EgressReceipt{}, err
	}
	payload := modelEgressPayload{
		ReceiptID: receiptID, TaskID: input.TaskID, ContractRevision: input.ContractRevision,
		ProviderKey: input.ProviderKey, ModelKey: input.ModelKey, RequestID: input.RequestID, PromptVersion: input.PromptVersion,
		ContextHash: input.ContextHash, RequestHash: input.RequestHash, ContextItems: input.ContextItems,
		InputBytes: input.InputBytes, RedactionCount: input.RedactionCount, Status: planning.EgressPrepared,
		ReservedInputTokens: input.ReservedInputTokens, ReservedOutputTokens: input.ReservedOutputTokens,
		CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano),
	}
	record, err := s.modelEgressEvent(input.VaultID, "model.egress.prepared", payload, now)
	if err != nil {
		return planning.EgressReceipt{}, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return planning.EgressReceipt{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO model_egress_receipts(
		receipt_id, vault_id, task_id, contract_revision, provider_key, model_key, request_id, prompt_version,
		context_hash, request_hash, response_hash, context_items, input_bytes, output_bytes, redaction_count,
		input_tokens, cached_input_tokens, output_tokens, reserved_input_tokens, reserved_output_tokens, status, safe_error_code, created_at, updated_at, created_event_id, last_event_id
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, 0, ?, 0, 0, 0, ?, ?, 'prepared', '', ?, ?, ?, ?)`,
		receiptID, input.VaultID, input.TaskID, input.ContractRevision, input.ProviderKey, input.ModelKey, input.RequestID,
		input.PromptVersion, input.ContextHash, input.RequestHash, input.ContextItems, input.InputBytes, input.RedactionCount,
		input.ReservedInputTokens, input.ReservedOutputTokens,
		payload.CreatedAt, payload.UpdatedAt, record.ID, record.ID,
	); err != nil {
		return planning.EgressReceipt{}, fmt.Errorf("insert model egress receipt: %w", err)
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, record.ID, requestHash); err != nil {
		return planning.EgressReceipt{}, err
	}
	if err := tx.Commit(); err != nil {
		return planning.EgressReceipt{}, fmt.Errorf("commit model egress preparation: %w", err)
	}
	return scanModelEgress(s.db.QueryRowContext(ctx, modelEgressSelect+" WHERE receipt_id = ?", receiptID))
}

func (s *Store) FinishModelEgress(ctx context.Context, input modelstore.FinishInput) (planning.EgressReceipt, error) {
	now := normalizedTime(input.OccurredAt, s.now)
	if input.VaultID == "" || input.ReceiptID == "" || input.ExpectedStatus != planning.EgressPrepared || (input.NextStatus != planning.EgressCompleted && input.NextStatus != planning.EgressFailed) || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 || input.Usage.Validate() != nil {
		return planning.EgressReceipt{}, modelstore.ErrInvalidCommand
	}
	if input.NextStatus == planning.EgressCompleted {
		if !validSHA256(input.ResponseHash) || !planning.ValidOpaqueID(input.ProviderCallID) || input.OutputBytes < 1 || input.OutputBytes > 1<<20 || input.SafeErrorCode != "" {
			return planning.EgressReceipt{}, modelstore.ErrInvalidCommand
		}
	} else if input.ResponseHash != "" || input.ProviderCallID != "" || input.OutputBytes != 0 || input.SafeErrorCode == "" || len(input.SafeErrorCode) > planning.MaxSafeErrorLength || input.Usage != (planning.Usage{}) {
		return planning.EgressReceipt{}, modelstore.ErrInvalidCommand
	}
	requestHash, err := modelEgressCommandHash(input)
	if err != nil {
		return planning.EgressReceipt{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return planning.EgressReceipt{}, fmt.Errorf("begin model egress finish: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existing, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, requestHash)
	if err != nil {
		return planning.EgressReceipt{}, mapModelEgressIdempotencyError(err)
	}
	if found {
		return scanModelEgress(tx.QueryRowContext(ctx, modelEgressSelect+" WHERE last_event_id = ?", existing.ID))
	}
	current, err := scanModelEgress(tx.QueryRowContext(ctx, modelEgressSelect+" WHERE receipt_id = ? AND vault_id = ?", input.ReceiptID, input.VaultID))
	if err != nil || current.Status != input.ExpectedStatus {
		return planning.EgressReceipt{}, modelstore.ErrConflict
	}
	updated := current
	updated.Status = input.NextStatus
	updated.ResponseHash = input.ResponseHash
	updated.ProviderCallID = input.ProviderCallID
	updated.OutputBytes = input.OutputBytes
	updated.Usage = input.Usage
	updated.ReservedInputTokens = 0
	updated.ReservedOutputTokens = 0
	updated.SafeErrorCode = input.SafeErrorCode
	updated.UpdatedAt = now
	updated.LastEventID = "pending-event"
	if updated.Validate() != nil {
		return planning.EgressReceipt{}, modelstore.ErrInvalidCommand
	}
	payload := modelEgressPayload{
		ReceiptID: current.ID, TaskID: current.TaskID, ContractRevision: current.ContractRevision,
		ProviderKey: current.ProviderKey, ModelKey: current.ModelKey, RequestID: current.RequestID, PromptVersion: current.PromptVersion,
		ContextHash: current.ContextHash, RequestHash: current.RequestHash, ResponseHash: input.ResponseHash, ProviderCallID: input.ProviderCallID,
		ContextItems: current.ContextItems, InputBytes: current.InputBytes, OutputBytes: input.OutputBytes,
		RedactionCount: current.RedactionCount, InputTokens: input.Usage.InputTokens,
		CachedInputTokens: input.Usage.CachedInputTokens, OutputTokens: input.Usage.OutputTokens,
		Status: input.NextStatus, SafeErrorCode: input.SafeErrorCode,
		CreatedAt: current.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano),
	}
	exceeded, err := settleModelBudget(ctx, tx, input.VaultID, current.TaskID, now, input.Budget, current.ReservedInputTokens, current.ReservedOutputTokens, input.Usage.InputTokens, input.Usage.OutputTokens)
	if err != nil {
		if errors.Is(err, errTaskBudgetExceeded) {
			return planning.EgressReceipt{}, modelstore.ErrBudgetExceeded
		}
		return planning.EgressReceipt{}, err
	}
	record, err := s.modelEgressEvent(input.VaultID, "model.egress.finished", payload, now)
	if err != nil {
		return planning.EgressReceipt{}, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return planning.EgressReceipt{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE model_egress_receipts SET response_hash = ?, provider_call_id = ?, output_bytes = ?, input_tokens = ?, cached_input_tokens = ?, output_tokens = ?, reserved_input_tokens = 0, reserved_output_tokens = 0, status = ?, safe_error_code = ?, updated_at = ?, last_event_id = ? WHERE receipt_id = ? AND status = 'prepared'`,
		input.ResponseHash, input.ProviderCallID, input.OutputBytes, input.Usage.InputTokens, input.Usage.CachedInputTokens, input.Usage.OutputTokens,
		string(input.NextStatus), input.SafeErrorCode, payload.UpdatedAt, record.ID, current.ID,
	)
	if err != nil {
		return planning.EgressReceipt{}, fmt.Errorf("update model egress receipt: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return planning.EgressReceipt{}, modelstore.ErrConflict
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, record.ID, requestHash); err != nil {
		return planning.EgressReceipt{}, err
	}
	if err := tx.Commit(); err != nil {
		return planning.EgressReceipt{}, fmt.Errorf("commit model egress finish: %w", err)
	}
	completed, err := scanModelEgress(s.db.QueryRowContext(ctx, modelEgressSelect+" WHERE receipt_id = ?", current.ID))
	if err != nil {
		return planning.EgressReceipt{}, err
	}
	if exceeded {
		return completed, modelstore.ErrBudgetExceeded
	}
	return completed, nil
}

func (s *Store) GetModelEgress(ctx context.Context, receiptID string) (planning.EgressReceipt, error) {
	if receiptID == "" {
		return planning.EgressReceipt{}, modelstore.ErrInvalidCommand
	}
	return scanModelEgress(s.db.QueryRowContext(ctx, modelEgressSelect+" WHERE receipt_id = ?", receiptID))
}

const modelEgressSelect = `SELECT receipt_id, vault_id, task_id, contract_revision, provider_key, model_key, request_id, prompt_version, context_hash, request_hash, response_hash, provider_call_id, context_items, input_bytes, output_bytes, redaction_count, input_tokens, cached_input_tokens, output_tokens, reserved_input_tokens, reserved_output_tokens, status, safe_error_code, created_at, updated_at, created_event_id, last_event_id FROM model_egress_receipts`

func scanModelEgress(row scanner) (planning.EgressReceipt, error) {
	var receipt planning.EgressReceipt
	var createdAt, updatedAt string
	if err := row.Scan(
		&receipt.ID, &receipt.VaultID, &receipt.TaskID, &receipt.ContractRevision, &receipt.ProviderKey, &receipt.ModelKey,
		&receipt.RequestID, &receipt.PromptVersion, &receipt.ContextHash, &receipt.RequestHash, &receipt.ResponseHash,
		&receipt.ProviderCallID,
		&receipt.ContextItems, &receipt.InputBytes, &receipt.OutputBytes, &receipt.RedactionCount,
		&receipt.Usage.InputTokens, &receipt.Usage.CachedInputTokens, &receipt.Usage.OutputTokens,
		&receipt.ReservedInputTokens, &receipt.ReservedOutputTokens,
		&receipt.Status, &receipt.SafeErrorCode, &createdAt, &updatedAt, &receipt.CreatedEventID, &receipt.LastEventID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return planning.EgressReceipt{}, modelstore.ErrNotFound
		}
		return planning.EgressReceipt{}, err
	}
	receipt.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	receipt.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	if receipt.Validate() != nil {
		return planning.EgressReceipt{}, modelstore.ErrConflict
	}
	return receipt, nil
}

func (s *Store) modelEgressEvent(vaultID, eventType string, payload modelEgressPayload, occurredAt time.Time) (event.Record, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return event.Record{}, err
	}
	return s.newEventRecord(vaultID, eventType, modelEgressEventSchemaVersion, event.SensitivityPrivate, encoded, occurredAt)
}

func modelEgressCommandHash(input any) ([]byte, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	return hash[:], nil
}

func mapModelEgressIdempotencyError(err error) error {
	if errors.Is(err, ErrIdempotencyConflict) || errors.Is(err, ErrIdempotencyUnverifiable) {
		return modelstore.ErrIdempotencyConflict
	}
	return err
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
