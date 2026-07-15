package sqliteevent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/accountlink"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/id"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/accountstore"
)

const accountLinkEventSchemaVersion = 1

type accountLinkPayload struct {
	MembershipID      string            `json:"membership_id"`
	VaultID           string            `json:"vault_id"`
	State             accountlink.State `json:"state"`
	Revision          int               `json:"revision"`
	SubjectRef        string            `json:"subject_ref,omitempty"`
	WorkspaceRef      string            `json:"workspace_ref,omitempty"`
	ConsentReceiptRef string            `json:"consent_receipt_ref,omitempty"`
	CreatedAt         string            `json:"created_at"`
	LinkedAt          string            `json:"linked_at,omitempty"`
	UpdatedAt         string            `json:"updated_at"`
	LastVerifiedAt    string            `json:"last_verified_at,omitempty"`
}

type accountLinkPointer struct {
	VaultID      string
	MembershipID string
	State        accountlink.State
	Revision     int
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastEventID  string
}

func (s *Store) LinkAccount(ctx context.Context, input accountstore.LinkInput) (accountlink.Record, error) {
	if input.VaultID == "" || input.ExpectedRevision < 0 || input.Identity.Validate() != nil || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 {
		return accountlink.Record{}, accountstore.ErrInvalidCommand
	}
	occurredAt := normalizedTime(input.OccurredAt, s.now)
	if occurredAt.Before(input.Identity.VerifiedAt) {
		occurredAt = input.Identity.VerifiedAt.UTC()
	}
	requestHash, err := accountCommandHash(input)
	if err != nil {
		return accountlink.Record{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return accountlink.Record{}, fmt.Errorf("begin account link: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existingEvent, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, requestHash)
	if err != nil {
		return accountlink.Record{}, mapAccountIdempotencyError(err)
	}
	if found {
		return s.accountLinkFromEvent(existingEvent)
	}
	if err := requireActiveVault(ctx, tx, input.VaultID); err != nil {
		return accountlink.Record{}, err
	}
	pointer, exists, err := getAccountLinkPointer(ctx, tx, input.VaultID)
	if err != nil {
		return accountlink.Record{}, err
	}
	if exists && (pointer.Revision != input.ExpectedRevision || pointer.State != accountlink.StateUnlinked) {
		return accountlink.Record{}, accountstore.ErrRevisionConflict
	}
	if !exists && input.ExpectedRevision != 0 {
		return accountlink.Record{}, accountstore.ErrRevisionConflict
	}
	membershipID := pointer.MembershipID
	createdAt := pointer.CreatedAt
	if !exists {
		membershipID, err = id.UUIDv7(occurredAt, s.random)
		if err != nil {
			return accountlink.Record{}, err
		}
		createdAt = occurredAt
	}
	payload := accountLinkPayload{MembershipID: membershipID, VaultID: input.VaultID, State: accountlink.StateLinked, Revision: input.ExpectedRevision + 1, SubjectRef: input.Identity.SubjectRef, WorkspaceRef: input.Identity.WorkspaceRef, ConsentReceiptRef: input.Identity.ConsentReceiptRef, CreatedAt: createdAt.Format(time.RFC3339Nano), LinkedAt: occurredAt.Format(time.RFC3339Nano), UpdatedAt: occurredAt.Format(time.RFC3339Nano), LastVerifiedAt: input.Identity.VerifiedAt.UTC().Format(time.RFC3339Nano)}
	eventRecord, err := s.accountLinkEvent("account.linked", payload, occurredAt)
	if err != nil {
		return accountlink.Record{}, err
	}
	if err := s.insertEvent(ctx, tx, eventRecord); err != nil {
		return accountlink.Record{}, err
	}
	if exists {
		result, err := tx.ExecContext(ctx, `UPDATE account_links SET state = 'linked', revision = ?, updated_at = ?, last_event_id = ? WHERE vault_id = ? AND revision = ? AND state = 'unlinked'`, payload.Revision, payload.UpdatedAt, eventRecord.ID, input.VaultID, input.ExpectedRevision)
		if err != nil {
			return accountlink.Record{}, err
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return accountlink.Record{}, accountstore.ErrRevisionConflict
		}
	} else if _, err := tx.ExecContext(ctx, `INSERT INTO account_links(vault_id, membership_id, state, revision, created_at, updated_at, last_event_id) VALUES(?, ?, 'linked', ?, ?, ?, ?)`, input.VaultID, membershipID, payload.Revision, payload.CreatedAt, payload.UpdatedAt, eventRecord.ID); err != nil {
		return accountlink.Record{}, fmt.Errorf("insert account link: %w", err)
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, eventRecord.ID, requestHash); err != nil {
		return accountlink.Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return accountlink.Record{}, err
	}
	return accountRecordFromPayload(payload, eventRecord.ID)
}

func (s *Store) UnlinkAccount(ctx context.Context, input accountstore.UnlinkInput) (accountlink.Record, error) {
	if input.VaultID == "" || input.ExpectedRevision < 1 || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 {
		return accountlink.Record{}, accountstore.ErrInvalidCommand
	}
	occurredAt := normalizedTime(input.OccurredAt, s.now)
	requestHash, err := accountCommandHash(input)
	if err != nil {
		return accountlink.Record{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return accountlink.Record{}, err
	}
	defer func() { _ = tx.Rollback() }()
	existingEvent, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, requestHash)
	if err != nil {
		return accountlink.Record{}, mapAccountIdempotencyError(err)
	}
	if found {
		return s.accountLinkFromEvent(existingEvent)
	}
	if err := requireActiveVault(ctx, tx, input.VaultID); err != nil {
		return accountlink.Record{}, err
	}
	pointer, exists, err := getAccountLinkPointer(ctx, tx, input.VaultID)
	if err != nil {
		return accountlink.Record{}, err
	}
	if !exists {
		return accountlink.Record{}, accountstore.ErrNotFound
	}
	if pointer.Revision != input.ExpectedRevision || pointer.State != accountlink.StateLinked {
		return accountlink.Record{}, accountstore.ErrRevisionConflict
	}
	payload := accountLinkPayload{MembershipID: pointer.MembershipID, VaultID: input.VaultID, State: accountlink.StateUnlinked, Revision: input.ExpectedRevision + 1, CreatedAt: pointer.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: occurredAt.Format(time.RFC3339Nano)}
	eventRecord, err := s.accountLinkEvent("account.unlinked", payload, occurredAt)
	if err != nil {
		return accountlink.Record{}, err
	}
	if err := s.insertEvent(ctx, tx, eventRecord); err != nil {
		return accountlink.Record{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE account_links SET state = 'unlinked', revision = ?, updated_at = ?, last_event_id = ? WHERE vault_id = ? AND revision = ? AND state = 'linked'`, payload.Revision, payload.UpdatedAt, eventRecord.ID, input.VaultID, input.ExpectedRevision)
	if err != nil {
		return accountlink.Record{}, err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return accountlink.Record{}, accountstore.ErrRevisionConflict
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, eventRecord.ID, requestHash); err != nil {
		return accountlink.Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return accountlink.Record{}, err
	}
	return accountRecordFromPayload(payload, eventRecord.ID)
}

func (s *Store) GetAccountLink(ctx context.Context, vaultID string) (accountlink.Record, error) {
	if vaultID == "" {
		return accountlink.Record{}, accountstore.ErrInvalidCommand
	}
	pointer, exists, err := getAccountLinkPointer(ctx, s.db, vaultID)
	if err != nil {
		return accountlink.Record{}, err
	}
	if !exists {
		return accountlink.Record{}, accountstore.ErrNotFound
	}
	record, err := s.Get(ctx, pointer.LastEventID)
	if err != nil {
		return accountlink.Record{}, err
	}
	return s.accountLinkFromEvent(record)
}

type accountLinkQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getAccountLinkPointer(ctx context.Context, queryer accountLinkQueryer, vaultID string) (accountLinkPointer, bool, error) {
	var pointer accountLinkPointer
	var state, createdAt, updatedAt string
	err := queryer.QueryRowContext(ctx, `SELECT vault_id, membership_id, state, revision, created_at, updated_at, last_event_id FROM account_links WHERE vault_id = ?`, vaultID).Scan(&pointer.VaultID, &pointer.MembershipID, &state, &pointer.Revision, &createdAt, &updatedAt, &pointer.LastEventID)
	if errors.Is(err, sql.ErrNoRows) {
		return accountLinkPointer{}, false, nil
	}
	if err != nil {
		return accountLinkPointer{}, false, err
	}
	pointer.State = accountlink.State(state)
	pointer.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	pointer.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return pointer, true, nil
}

func (s *Store) accountLinkEvent(eventType string, payload accountLinkPayload, occurredAt time.Time) (event.Record, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return event.Record{}, err
	}
	return s.newEventRecord(payload.VaultID, eventType, accountLinkEventSchemaVersion, event.SensitivityPrivate, encoded, occurredAt)
}

func (s *Store) accountLinkFromEvent(record event.Record) (accountlink.Record, error) {
	if record.Type != "account.linked" && record.Type != "account.unlinked" || record.SchemaVersion != accountLinkEventSchemaVersion || record.Sensitivity != event.SensitivityPrivate {
		return accountlink.Record{}, accountstore.ErrNotFound
	}
	var payload accountLinkPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return accountlink.Record{}, accountstore.ErrNotFound
	}
	if payload.VaultID != record.VaultID {
		return accountlink.Record{}, accountstore.ErrNotFound
	}
	return accountRecordFromPayload(payload, record.ID)
}

func accountRecordFromPayload(payload accountLinkPayload, eventID string) (accountlink.Record, error) {
	createdAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
	if err != nil {
		return accountlink.Record{}, accountstore.ErrInvalidCommand
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, payload.UpdatedAt)
	if err != nil {
		return accountlink.Record{}, accountstore.ErrInvalidCommand
	}
	record := accountlink.Record{MembershipID: payload.MembershipID, VaultID: payload.VaultID, State: payload.State, Revision: payload.Revision, SubjectRef: payload.SubjectRef, WorkspaceRef: payload.WorkspaceRef, ConsentReceiptRef: payload.ConsentReceiptRef, CreatedAt: createdAt, UpdatedAt: updatedAt, LastEventID: eventID}
	if payload.LinkedAt != "" {
		record.LinkedAt, _ = time.Parse(time.RFC3339Nano, payload.LinkedAt)
	}
	if payload.LastVerifiedAt != "" {
		record.LastVerifiedAt, _ = time.Parse(time.RFC3339Nano, payload.LastVerifiedAt)
	}
	if err := record.Validate(); err != nil {
		return accountlink.Record{}, accountstore.ErrInvalidCommand
	}
	return record, nil
}

func accountCommandHash(input any) ([]byte, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	return hash[:], nil
}

func mapAccountIdempotencyError(err error) error {
	switch {
	case errors.Is(err, ErrIdempotencyConflict):
		return accountstore.ErrIdempotencyConflict
	case errors.Is(err, ErrIdempotencyUnverifiable):
		return accountstore.ErrIdempotencyUnverified
	default:
		return err
	}
}
