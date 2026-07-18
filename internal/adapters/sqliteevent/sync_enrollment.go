package sqliteevent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/syncenrollment"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/enrollmentstore"
	"github.com/0disoft/zdp-desktop-talos/internal/security/envelope"
)

const syncEnrollmentEventSchemaVersion = 1

type syncEnrollmentPayload struct {
	EnrollmentID   string               `json:"enrollment_id"`
	VaultID        string               `json:"vault_id"`
	Role           syncenrollment.Role  `json:"role"`
	State          syncenrollment.State `json:"state"`
	PeerDeviceID   string               `json:"peer_device_id,omitempty"`
	OfferHash      string               `json:"offer_hash"`
	AcceptanceHash string               `json:"acceptance_hash,omitempty"`
	ExpiresAt      string               `json:"expires_at"`
	CreatedAt      string               `json:"created_at"`
	UpdatedAt      string               `json:"updated_at"`
}

func (s *Store) RecordEnrollmentOffer(ctx context.Context, input enrollmentstore.RecordOfferInput) (syncenrollment.Record, bool, error) {
	occurredAt := normalizedTime(input.OccurredAt, s.now)
	candidate := syncenrollment.Record{EnrollmentID: input.EnrollmentID, VaultID: input.VaultID, Role: syncenrollment.RoleIssuer, State: syncenrollment.StateOffered, OfferHash: input.OfferHash, ExpiresAt: input.ExpiresAt.UTC(), CreatedAt: occurredAt, UpdatedAt: occurredAt, CreatedEventID: "pending", LastEventID: "pending"}
	if candidate.Validate() != nil || !occurredAt.Before(candidate.ExpiresAt) {
		return syncenrollment.Record{}, false, enrollmentstore.ErrInvalidCommand
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return syncenrollment.Record{}, false, fmt.Errorf("begin sync enrollment offer: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stored, _, found, err := s.getEnrollment(ctx, tx, input.VaultID, input.EnrollmentID)
	if err != nil {
		return syncenrollment.Record{}, false, err
	}
	if found {
		if stored.Role == syncenrollment.RoleIssuer && stored.State == syncenrollment.StateOffered && stored.OfferHash == input.OfferHash && stored.ExpiresAt.Equal(input.ExpiresAt.UTC()) {
			return stored, true, nil
		}
		return syncenrollment.Record{}, false, enrollmentstore.ErrConflict
	}
	if err := requireActiveVault(ctx, tx, input.VaultID); err != nil {
		return syncenrollment.Record{}, false, err
	}
	payload := enrollmentPayload(candidate)
	record, err := s.enrollmentEvent("sync.enrollment.offered", input.VaultID, payload, occurredAt)
	if err != nil {
		return syncenrollment.Record{}, false, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return syncenrollment.Record{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_enrollments(enrollment_id, vault_id, role, state, peer_device_id, offer_hash, acceptance_hash, acceptance_envelope, expires_at, created_at, updated_at, created_event_id, last_event_id) VALUES(?, ?, 'issuer', 'offered', NULL, ?, NULL, NULL, ?, ?, ?, ?, ?)`, input.EnrollmentID, input.VaultID, input.OfferHash, payload.ExpiresAt, payload.CreatedAt, payload.UpdatedAt, record.ID, record.ID); err != nil {
		return syncenrollment.Record{}, false, fmt.Errorf("insert sync enrollment offer: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return syncenrollment.Record{}, false, fmt.Errorf("commit sync enrollment offer: %w", err)
	}
	candidate.CreatedEventID = record.ID
	candidate.LastEventID = record.ID
	return candidate, false, nil
}

func (s *Store) CancelEnrollment(ctx context.Context, input enrollmentstore.CancelInput) (syncenrollment.Record, bool, error) {
	if input.EnrollmentID == "" || input.VaultID == "" {
		return syncenrollment.Record{}, false, enrollmentstore.ErrInvalidCommand
	}
	return s.transitionEnrollmentTerminal(ctx, input.VaultID, input.EnrollmentID, syncenrollment.StateCanceled, normalizedTime(input.OccurredAt, s.now))
}

func (s *Store) ExpireEnrollments(ctx context.Context, vaultID string, asOf time.Time) ([]syncenrollment.Record, error) {
	if vaultID == "" || asOf.IsZero() {
		return nil, enrollmentstore.ErrInvalidCommand
	}
	return s.expireEnrollments(ctx, vaultID, asOf.UTC())
}

func (s *Store) ReconcileExpiredEnrollments(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT vault_id FROM sync_enrollments WHERE state IN ('offered','accepted') AND expires_at <= ? ORDER BY vault_id`, s.now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("list Vaults with expired sync enrollments: %w", err)
	}
	var vaultIDs []string
	for rows.Next() {
		var vaultID string
		if err := rows.Scan(&vaultID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan Vault with expired sync enrollments: %w", err)
		}
		vaultIDs = append(vaultIDs, vaultID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate Vaults with expired sync enrollments: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close expired sync enrollment Vault cursor: %w", err)
	}
	for _, vaultID := range vaultIDs {
		if _, err := s.expireEnrollments(ctx, vaultID, s.now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) expireEnrollments(ctx context.Context, vaultID string, asOf time.Time) ([]syncenrollment.Record, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT enrollment_id FROM sync_enrollments WHERE vault_id = ? AND state IN ('offered','accepted') AND expires_at <= ? ORDER BY expires_at, enrollment_id`, vaultID, asOf.Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("list expired sync enrollments: %w", err)
	}
	var enrollmentIDs []string
	for rows.Next() {
		var enrollmentID string
		if err := rows.Scan(&enrollmentID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan expired sync enrollment: %w", err)
		}
		enrollmentIDs = append(enrollmentIDs, enrollmentID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate expired sync enrollments: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close expired sync enrollment cursor: %w", err)
	}
	transitioned := make([]syncenrollment.Record, 0, len(enrollmentIDs))
	for _, enrollmentID := range enrollmentIDs {
		record, replay, err := s.transitionEnrollmentTerminal(ctx, vaultID, enrollmentID, syncenrollment.StateExpired, asOf)
		if errors.Is(err, enrollmentstore.ErrConflict) || errors.Is(err, enrollmentstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !replay {
			transitioned = append(transitioned, record)
		}
	}
	return transitioned, nil
}

func (s *Store) transitionEnrollmentTerminal(ctx context.Context, vaultID, enrollmentID string, next syncenrollment.State, occurredAt time.Time) (syncenrollment.Record, bool, error) {
	if next != syncenrollment.StateCanceled && next != syncenrollment.StateExpired {
		return syncenrollment.Record{}, false, enrollmentstore.ErrInvalidCommand
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return syncenrollment.Record{}, false, fmt.Errorf("begin sync enrollment terminal transition: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stored, response, found, err := s.getEnrollment(ctx, tx, vaultID, enrollmentID)
	clear(response)
	if err != nil {
		return syncenrollment.Record{}, false, err
	}
	if !found {
		return syncenrollment.Record{}, false, enrollmentstore.ErrNotFound
	}
	if stored.State == next {
		return stored, true, nil
	}
	if !stored.CanCancel() || (next == syncenrollment.StateExpired && !stored.CanExpireAt(occurredAt)) || (next == syncenrollment.StateCanceled && !occurredAt.Before(stored.ExpiresAt)) {
		return syncenrollment.Record{}, false, enrollmentstore.ErrConflict
	}
	if err := requireActiveVault(ctx, tx, vaultID); err != nil {
		return syncenrollment.Record{}, false, err
	}
	previous := stored.State
	stored.State = next
	stored.UpdatedAt = occurredAt
	if stored.Validate() != nil {
		return syncenrollment.Record{}, false, enrollmentstore.ErrInvalidCommand
	}
	payload := enrollmentPayload(stored)
	record, err := s.enrollmentEvent("sync.enrollment."+string(next), vaultID, payload, occurredAt)
	if err != nil {
		return syncenrollment.Record{}, false, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return syncenrollment.Record{}, false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE sync_enrollments SET state = ?, acceptance_envelope = NULL, updated_at = ?, last_event_id = ? WHERE vault_id = ? AND enrollment_id = ? AND state = ?`, string(next), payload.UpdatedAt, record.ID, vaultID, enrollmentID, string(previous))
	if err != nil {
		return syncenrollment.Record{}, false, fmt.Errorf("update sync enrollment terminal state: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return syncenrollment.Record{}, false, enrollmentstore.ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return syncenrollment.Record{}, false, fmt.Errorf("commit sync enrollment terminal transition: %w", err)
	}
	stored.LastEventID = record.ID
	return stored, false, nil
}

func (s *Store) RecordEnrollmentAcceptance(ctx context.Context, input enrollmentstore.RecordAcceptanceInput) (syncenrollment.Record, []byte, bool, error) {
	occurredAt := normalizedTime(input.OccurredAt, s.now)
	candidate := syncenrollment.Record{EnrollmentID: input.EnrollmentID, VaultID: input.VaultID, Role: syncenrollment.RoleRecipient, State: syncenrollment.StateAccepted, PeerDeviceID: input.SourceDeviceID, OfferHash: input.OfferHash, AcceptanceHash: input.AcceptanceHash, ExpiresAt: input.ExpiresAt.UTC(), CreatedAt: occurredAt, UpdatedAt: occurredAt, CreatedEventID: "pending", LastEventID: "pending"}
	hash := sha256.Sum256(input.EncodedAcceptance)
	if candidate.Validate() != nil || len(input.EncodedAcceptance) == 0 || len(input.EncodedAcceptance) > syncenrollment.MaxPackageBytes || hex.EncodeToString(hash[:]) != input.AcceptanceHash || occurredAt.After(candidate.ExpiresAt) {
		return syncenrollment.Record{}, nil, false, enrollmentstore.ErrInvalidCommand
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return syncenrollment.Record{}, nil, false, fmt.Errorf("begin sync enrollment acceptance: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stored, response, found, err := s.getEnrollment(ctx, tx, input.VaultID, input.EnrollmentID)
	if err != nil {
		return syncenrollment.Record{}, nil, false, err
	}
	if found {
		if stored.Role == syncenrollment.RoleRecipient && stored.State == syncenrollment.StateAccepted && stored.PeerDeviceID == input.SourceDeviceID && stored.OfferHash == input.OfferHash && stored.AcceptanceHash == input.AcceptanceHash && bytes.Equal(response, input.EncodedAcceptance) {
			return stored, response, true, nil
		}
		clear(response)
		return syncenrollment.Record{}, nil, false, enrollmentstore.ErrConflict
	}
	if err := requireActiveVault(ctx, tx, input.VaultID); err != nil {
		return syncenrollment.Record{}, nil, false, err
	}
	encrypted, err := s.sealer.Seal(input.EncodedAcceptance, enrollmentResponseAAD(input.VaultID, input.EnrollmentID))
	if err != nil {
		return syncenrollment.Record{}, nil, false, fmt.Errorf("encrypt sync enrollment acceptance: %w", err)
	}
	payload := enrollmentPayload(candidate)
	record, err := s.enrollmentEvent("sync.enrollment.accepted", input.VaultID, payload, occurredAt)
	if err != nil {
		return syncenrollment.Record{}, nil, false, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return syncenrollment.Record{}, nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sync_enrollments(enrollment_id, vault_id, role, state, peer_device_id, offer_hash, acceptance_hash, acceptance_envelope, expires_at, created_at, updated_at, created_event_id, last_event_id) VALUES(?, ?, 'recipient', 'accepted', ?, ?, ?, ?, ?, ?, ?, ?, ?)`, input.EnrollmentID, input.VaultID, input.SourceDeviceID, input.OfferHash, input.AcceptanceHash, encrypted, payload.ExpiresAt, payload.CreatedAt, payload.UpdatedAt, record.ID, record.ID); err != nil {
		return syncenrollment.Record{}, nil, false, fmt.Errorf("insert sync enrollment acceptance: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return syncenrollment.Record{}, nil, false, fmt.Errorf("commit sync enrollment acceptance: %w", err)
	}
	candidate.CreatedEventID = record.ID
	candidate.LastEventID = record.ID
	return candidate, append([]byte(nil), input.EncodedAcceptance...), false, nil
}

func (s *Store) CompleteEnrollment(ctx context.Context, input enrollmentstore.CompleteInput) (syncenrollment.Record, bool, error) {
	if input.EnrollmentID == "" || input.VaultID == "" || input.TargetDeviceID == "" || input.OfferHash == "" || input.AcceptanceHash == "" {
		return syncenrollment.Record{}, false, enrollmentstore.ErrInvalidCommand
	}
	occurredAt := normalizedTime(input.OccurredAt, s.now)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return syncenrollment.Record{}, false, fmt.Errorf("begin sync enrollment completion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stored, response, found, err := s.getEnrollment(ctx, tx, input.VaultID, input.EnrollmentID)
	clear(response)
	if err != nil {
		return syncenrollment.Record{}, false, err
	}
	if !found {
		return syncenrollment.Record{}, false, enrollmentstore.ErrNotFound
	}
	if stored.Role != syncenrollment.RoleIssuer || stored.OfferHash != input.OfferHash {
		return syncenrollment.Record{}, false, enrollmentstore.ErrConflict
	}
	if stored.State == syncenrollment.StateCompleted {
		if stored.PeerDeviceID == input.TargetDeviceID && stored.AcceptanceHash == input.AcceptanceHash {
			return stored, true, nil
		}
		return syncenrollment.Record{}, false, enrollmentstore.ErrConflict
	}
	if stored.State != syncenrollment.StateOffered || occurredAt.After(stored.ExpiresAt) {
		return syncenrollment.Record{}, false, enrollmentstore.ErrConflict
	}
	stored.State = syncenrollment.StateCompleted
	stored.PeerDeviceID = input.TargetDeviceID
	stored.AcceptanceHash = input.AcceptanceHash
	stored.UpdatedAt = occurredAt
	if stored.Validate() != nil {
		return syncenrollment.Record{}, false, enrollmentstore.ErrInvalidCommand
	}
	payload := enrollmentPayload(stored)
	record, err := s.enrollmentEvent("sync.enrollment.completed", input.VaultID, payload, occurredAt)
	if err != nil {
		return syncenrollment.Record{}, false, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return syncenrollment.Record{}, false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE sync_enrollments SET state = 'completed', peer_device_id = ?, acceptance_hash = ?, updated_at = ?, last_event_id = ? WHERE vault_id = ? AND enrollment_id = ? AND role = 'issuer' AND state = 'offered' AND offer_hash = ?`, input.TargetDeviceID, input.AcceptanceHash, payload.UpdatedAt, record.ID, input.VaultID, input.EnrollmentID, input.OfferHash)
	if err != nil {
		return syncenrollment.Record{}, false, err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return syncenrollment.Record{}, false, enrollmentstore.ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return syncenrollment.Record{}, false, fmt.Errorf("commit sync enrollment completion: %w", err)
	}
	stored.LastEventID = record.ID
	return stored, false, nil
}

func (s *Store) GetEnrollment(ctx context.Context, vaultID, enrollmentID string) (syncenrollment.Record, []byte, error) {
	record, response, found, err := s.getEnrollment(ctx, s.db, vaultID, enrollmentID)
	if err != nil {
		return syncenrollment.Record{}, nil, err
	}
	if !found {
		return syncenrollment.Record{}, nil, enrollmentstore.ErrNotFound
	}
	return record, response, nil
}

type enrollmentQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) getEnrollment(ctx context.Context, queryer enrollmentQueryer, vaultID, enrollmentID string) (syncenrollment.Record, []byte, bool, error) {
	var record syncenrollment.Record
	var role, state, expiresAt, createdAt, updatedAt string
	var peerDeviceID, acceptanceHash sql.NullString
	var encrypted []byte
	err := queryer.QueryRowContext(ctx, `SELECT enrollment_id, vault_id, role, state, peer_device_id, offer_hash, acceptance_hash, acceptance_envelope, expires_at, created_at, updated_at, created_event_id, last_event_id FROM sync_enrollments WHERE vault_id = ? AND enrollment_id = ?`, vaultID, enrollmentID).Scan(&record.EnrollmentID, &record.VaultID, &role, &state, &peerDeviceID, &record.OfferHash, &acceptanceHash, &encrypted, &expiresAt, &createdAt, &updatedAt, &record.CreatedEventID, &record.LastEventID)
	if errors.Is(err, sql.ErrNoRows) {
		return syncenrollment.Record{}, nil, false, nil
	}
	if err != nil {
		return syncenrollment.Record{}, nil, false, err
	}
	record.Role = syncenrollment.Role(role)
	record.State = syncenrollment.State(state)
	if peerDeviceID.Valid {
		record.PeerDeviceID = peerDeviceID.String
	}
	if acceptanceHash.Valid {
		record.AcceptanceHash = acceptanceHash.String
	}
	record.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expiresAt)
	record.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	record.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	if record.Validate() != nil {
		return syncenrollment.Record{}, nil, false, enrollmentstore.ErrInvalidCommand
	}
	if record.Role == syncenrollment.RoleRecipient && record.State == syncenrollment.StateAccepted {
		if len(encrypted) == 0 {
			return syncenrollment.Record{}, nil, false, enrollmentstore.ErrResponseUnavailable
		}
		response, err := s.sealer.Open(encrypted, enrollmentResponseAAD(record.VaultID, record.EnrollmentID))
		if err != nil {
			return syncenrollment.Record{}, nil, false, fmt.Errorf("decrypt sync enrollment acceptance: %w", err)
		}
		hash := sha256.Sum256(response)
		if hex.EncodeToString(hash[:]) != record.AcceptanceHash || len(response) > syncenrollment.MaxPackageBytes {
			clear(response)
			return syncenrollment.Record{}, nil, false, enrollmentstore.ErrResponseUnavailable
		}
		return record, response, true, nil
	}
	return record, nil, true, nil
}

func (s *Store) enrollmentEvent(eventType, vaultID string, payload syncEnrollmentPayload, occurredAt time.Time) (event.Record, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return event.Record{}, err
	}
	return s.newEventRecord(vaultID, eventType, syncEnrollmentEventSchemaVersion, event.SensitivityPrivate, encoded, occurredAt)
}

func enrollmentPayload(record syncenrollment.Record) syncEnrollmentPayload {
	return syncEnrollmentPayload{EnrollmentID: record.EnrollmentID, VaultID: record.VaultID, Role: record.Role, State: record.State, PeerDeviceID: record.PeerDeviceID, OfferHash: record.OfferHash, AcceptanceHash: record.AcceptanceHash, ExpiresAt: record.ExpiresAt.UTC().Format(time.RFC3339Nano), CreatedAt: record.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: record.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}

func enrollmentResponseAAD(vaultID, enrollmentID string) envelope.AAD {
	return envelope.AAD{VaultID: vaultID, ObjectID: "sync-enrollment-acceptance:" + enrollmentID, SchemaVersion: 1, Sensitivity: "sensitive"}
}
