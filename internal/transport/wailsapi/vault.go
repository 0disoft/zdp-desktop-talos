package wailsapi

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultbackup"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
	"github.com/0disoft/zdp-desktop-talos/internal/version"
)

type VaultStatus struct {
	State              string `json:"state"`
	PersistentKeyStore bool   `json:"persistent_key_store"`
	VaultID            string `json:"vault_id,omitempty"`
	Revision           int    `json:"revision,omitempty"`
	RetentionDays      int    `json:"retention_days,omitempty"`
}

type VaultResult struct {
	Vault *VaultStatus `json:"vault,omitempty"`
	Error *TalosError  `json:"error,omitempty"`
}

type VaultSummary struct {
	VaultID   string `json:"vault_id"`
	CreatedAt string `json:"created_at"`
}

type VaultListResult struct {
	Vaults []VaultSummary `json:"vaults,omitempty"`
	Error  *TalosError    `json:"error,omitempty"`
}

type VaultBackupReceiptView struct {
	Schema                   string `json:"schema"`
	BackupID                 string `json:"backup_id"`
	VaultID                  string `json:"vault_id"`
	Path                     string `json:"path"`
	SourceApplicationVersion string `json:"source_application_version"`
	SourceSchemaVersion      int    `json:"source_schema_version"`
	CreatedAt                string `json:"created_at"`
	EncryptedSizeBytes       int64  `json:"encrypted_size_bytes"`
	CiphertextSHA256         string `json:"ciphertext_sha256"`
	DatabaseSizeBytes        int64  `json:"database_size_bytes"`
	ArtifactCount            int    `json:"artifact_count"`
	EventCount               int    `json:"event_count"`
}

type VaultBackupPreflightView struct {
	Schema                   string `json:"schema"`
	BackupID                 string `json:"backup_id"`
	VaultID                  string `json:"vault_id"`
	Path                     string `json:"path"`
	SourceApplicationVersion string `json:"source_application_version"`
	MinimumRestoreVersion    string `json:"minimum_restore_version"`
	TargetApplicationVersion string `json:"target_application_version"`
	SourceSchemaVersion      int    `json:"source_schema_version"`
	TargetSchemaVersion      int    `json:"target_schema_version"`
	MigrationRequired        bool   `json:"migration_required"`
	CreatedAt                string `json:"created_at"`
	VerifiedAt               string `json:"verified_at"`
	EncryptedSizeBytes       int64  `json:"encrypted_size_bytes"`
	CiphertextSHA256         string `json:"ciphertext_sha256"`
	DatabaseSizeBytes        int64  `json:"database_size_bytes"`
	ArtifactCount            int    `json:"artifact_count"`
	EventCount               int    `json:"event_count"`
}

type VaultBackupResult struct {
	Receipt *VaultBackupReceiptView `json:"receipt,omitempty"`
	Error   *TalosError             `json:"error,omitempty"`
}

type VaultBackupPreflightResult struct {
	Preflight *VaultBackupPreflightView `json:"preflight,omitempty"`
	Error     *TalosError               `json:"error,omitempty"`
}

type VaultRestoreView struct {
	Schema                   string `json:"schema"`
	State                    string `json:"state"`
	BackupID                 string `json:"backup_id"`
	VaultID                  string `json:"vault_id"`
	CiphertextSHA256         string `json:"ciphertext_sha256"`
	SourceApplicationVersion string `json:"source_application_version"`
	TargetApplicationVersion string `json:"target_application_version"`
	SourceSchemaVersion      int    `json:"source_schema_version"`
	TargetSchemaVersion      int    `json:"target_schema_version"`
	RestoredAt               string `json:"restored_at"`
	ArtifactCount            int    `json:"artifact_count"`
	EventCount               int    `json:"event_count"`
}

type VaultRestoreResult struct {
	Restore *VaultRestoreView `json:"restore,omitempty"`
	Vault   *VaultStatus      `json:"vault,omitempty"`
	Error   *TalosError       `json:"error,omitempty"`
}

type VaultService struct {
	mu                  sync.Mutex
	leaseCond           *sync.Cond
	leases              map[uint64]context.CancelFunc
	nextLeaseID         uint64
	closing             bool
	creator             *vaultbootstrap.Creator
	initializationError error
	session             *vaultbootstrap.Session
}

func NewVaultService(creator *vaultbootstrap.Creator, initializationError error) *VaultService {
	service := &VaultService{creator: creator, initializationError: initializationError, leases: make(map[uint64]context.CancelFunc)}
	service.leaseCond = sync.NewCond(&service.mu)
	return service
}

type vaultSessionLease struct {
	Session *vaultbootstrap.Session
	Context context.Context
	release func()
}

func (l *vaultSessionLease) Release() {
	if l != nil && l.release != nil {
		l.release()
	}
}

func (s *VaultService) acquireSessionLease(parent context.Context) (*vaultSessionLease, error) {
	if s == nil || parent == nil {
		return nil, vaultbootstrap.ErrNotOpen
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil || s.closing {
		return nil, vaultbootstrap.ErrNotOpen
	}
	ctx, cancel := context.WithCancel(parent)
	s.nextLeaseID++
	id := s.nextLeaseID
	s.leases[id] = cancel
	var once sync.Once
	return &vaultSessionLease{Session: s.session, Context: ctx, release: func() {
		once.Do(func() {
			cancel()
			s.mu.Lock()
			delete(s.leases, id)
			s.leaseCond.Broadcast()
			s.mu.Unlock()
		})
	}}, nil
}

func (s *VaultService) quiesceLeasesLocked() {
	s.closing = true
	for _, cancel := range s.leases {
		cancel()
	}
	for len(s.leases) > 0 {
		s.leaseCond.Wait()
	}
}

func (s *VaultService) waitForCloseLocked() {
	for s.closing {
		s.leaseCond.Wait()
	}
}

func (s *VaultService) Status() VaultStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

func (s *VaultService) Create(retentionDays int, correlationID string) VaultResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.creator == nil {
		mapped := s.unavailableError(correlationID)
		return VaultResult{Error: &mapped}
	}
	if s.session != nil {
		mapped := TalosError{Code: "VAULT_ALREADY_OPEN", Message: "현재 열린 Vault를 먼저 잠가 주세요.", CorrelationID: normalizeCorrelationID(correlationID)}
		return VaultResult{Error: &mapped}
	}
	session, err := s.creator.Create(context.Background(), vaultbootstrap.CreateInput{RetentionDays: retentionDays})
	if err != nil {
		mapped := MapError(err, correlationID)
		return VaultResult{Error: &mapped}
	}
	s.session = session
	status := s.statusLocked()
	return VaultResult{Vault: &status}
}

func (s *VaultService) List(correlationID string) VaultListResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.creator == nil {
		mapped := s.unavailableError(correlationID)
		return VaultListResult{Error: &mapped}
	}
	entries, err := s.creator.List(context.Background())
	if err != nil {
		mapped := MapError(err, correlationID)
		return VaultListResult{Error: &mapped}
	}
	result := VaultListResult{Vaults: make([]VaultSummary, 0, len(entries))}
	for _, entry := range entries {
		result.Vaults = append(result.Vaults, VaultSummary{VaultID: entry.VaultID, CreatedAt: entry.CreatedAt.UTC().Format(time.RFC3339Nano)})
	}
	return result
}

func (s *VaultService) Open(vaultID, correlationID string) VaultResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.creator == nil {
		mapped := s.unavailableError(correlationID)
		return VaultResult{Error: &mapped}
	}
	if s.session != nil {
		mapped := TalosError{Code: "VAULT_ALREADY_OPEN", Message: "현재 열린 Vault를 먼저 잠가 주세요.", CorrelationID: normalizeCorrelationID(correlationID)}
		return VaultResult{Error: &mapped}
	}
	session, err := s.creator.Open(context.Background(), vaultID)
	if err != nil {
		mapped := MapError(err, correlationID)
		return VaultResult{Error: &mapped}
	}
	s.session = session
	status := s.statusLocked()
	return VaultResult{Vault: &status}
}

func (s *VaultService) UpdateRetention(retentionDays, expectedRevision int, requestID, correlationID string) VaultResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		mapped := TalosError{Code: "VAULT_NOT_OPEN", Message: "보존 기간을 바꾸려면 Vault를 먼저 열어 주세요.", CorrelationID: normalizeCorrelationID(correlationID)}
		return VaultResult{Error: &mapped}
	}
	requestID = normalizeCorrelationID(requestID)
	if requestID == "" {
		mapped := TalosError{Code: "VAULT_INPUT_INVALID", Message: "Vault 설정값을 확인해 주세요.", CorrelationID: normalizeCorrelationID(correlationID)}
		return VaultResult{Error: &mapped}
	}
	_, err := s.session.UpdateRetention(context.Background(), vaultbootstrap.UpdateRetentionInput{
		ExpectedRevision: expectedRevision,
		RetentionDays:    retentionDays,
		IdempotencyKey:   "vault-retention:" + requestID,
	})
	if err != nil {
		mapped := MapError(err, correlationID)
		return VaultResult{Error: &mapped}
	}
	status := s.statusLocked()
	return VaultResult{Vault: &status}
}

func (s *VaultService) HardPurge(expectedRevision int, confirmation, correlationID string) VaultResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waitForCloseLocked()
	if s.session == nil {
		mapped := TalosError{Code: "VAULT_NOT_OPEN", Message: "완전 삭제할 Vault를 먼저 열어 주세요.", CorrelationID: normalizeCorrelationID(correlationID)}
		return VaultResult{Error: &mapped}
	}
	if expectedRevision != s.session.Record.Revision {
		mapped := MapError(vaultstore.ErrRevisionConflict, correlationID)
		return VaultResult{Error: &mapped}
	}
	if confirmation != s.session.Record.ID {
		mapped := MapError(vaultbootstrap.ErrInvalidInput, correlationID)
		return VaultResult{Error: &mapped}
	}
	s.quiesceLeasesLocked()
	err := s.creator.HardPurge(context.Background(), s.session, vaultbootstrap.HardPurgeInput{
		ExpectedRevision: expectedRevision,
		Confirmation:     confirmation,
	})
	s.session = nil
	s.closing = false
	s.leaseCond.Broadcast()
	if err != nil {
		mapped := MapError(err, correlationID)
		return VaultResult{Error: &mapped}
	}
	status := s.statusLocked()
	return VaultResult{Vault: &status}
}

func (s *VaultService) CreateBackup(destination, correlationID string) VaultBackupResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		mapped := MapError(vaultbootstrap.ErrNotOpen, correlationID)
		return VaultBackupResult{Error: &mapped}
	}
	receipt, err := s.session.CreateBackup(context.Background(), destination, version.Application)
	if err != nil {
		mapped := MapError(err, correlationID)
		return VaultBackupResult{Error: &mapped}
	}
	view := backupReceiptView(receipt)
	return VaultBackupResult{Receipt: &view}
}

func (s *VaultService) PreflightBackup(source, correlationID string) VaultBackupPreflightResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		mapped := MapError(vaultbootstrap.ErrNotOpen, correlationID)
		return VaultBackupPreflightResult{Error: &mapped}
	}
	preflight, err := s.session.PreflightBackup(context.Background(), source, version.Application)
	if err != nil {
		mapped := MapError(err, correlationID)
		return VaultBackupPreflightResult{Error: &mapped}
	}
	view := backupPreflightView(preflight)
	return VaultBackupPreflightResult{Preflight: &view}
}

func (s *VaultService) RestoreBackup(source, expectedBackupID, expectedCiphertextSHA256 string, expectedRevision int, confirmation, correlationID string) VaultRestoreResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		mapped := MapError(vaultbootstrap.ErrNotOpen, correlationID)
		return VaultRestoreResult{Error: &mapped}
	}
	restored, err := s.creator.RestoreBackup(context.Background(), s.session, vaultbootstrap.RestoreBackupInput{
		Source: source, ExpectedBackupID: expectedBackupID, ExpectedCiphertextSHA256: expectedCiphertextSHA256,
		ExpectedRevision: expectedRevision, Confirmation: confirmation, ApplicationVersion: version.Application,
	})
	s.session = restored.Session
	result := VaultRestoreResult{}
	if restored.Restore.Schema != "" {
		view := backupRestoreView(restored.Restore)
		result.Restore = &view
	}
	if s.session != nil {
		status := s.statusLocked()
		result.Vault = &status
	}
	if err != nil {
		mapped := MapError(err, correlationID)
		result.Error = &mapped
	}
	return result
}

func (s *VaultService) Lock(correlationID string) VaultResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waitForCloseLocked()
	if s.session == nil {
		status := s.statusLocked()
		return VaultResult{Vault: &status}
	}
	s.quiesceLeasesLocked()
	err := s.session.Close()
	s.session = nil
	s.closing = false
	s.leaseCond.Broadcast()
	if err != nil {
		mapped := MapError(err, correlationID)
		mapped.Code = "VAULT_LOCK_FAILED"
		mapped.Message = "Vault를 안전하게 잠그지 못했습니다."
		return VaultResult{Error: &mapped}
	}
	status := s.statusLocked()
	return VaultResult{Vault: &status}
}

func (s *VaultService) statusLocked() VaultStatus {
	status := VaultStatus{State: "locked", PersistentKeyStore: s.creator != nil}
	if s.session != nil {
		status.State = "unlocked"
		status.VaultID = s.session.Record.ID
		status.Revision = s.session.Record.Revision
		status.RetentionDays = s.session.Record.RetentionDays
	}
	return status
}

func (s *VaultService) unavailableError(correlationID string) TalosError {
	err := s.initializationError
	if err == nil {
		err = errors.New("Vault creator is unavailable")
	}
	mapped := MapError(err, correlationID)
	mapped.Code = "VAULT_STORAGE_UNAVAILABLE"
	mapped.Message = "이 기기에서 안전한 Vault 저장소를 사용할 수 없습니다."
	return mapped
}

func backupReceiptView(receipt vaultbackup.Receipt) VaultBackupReceiptView {
	return VaultBackupReceiptView{
		Schema: receipt.Schema, BackupID: receipt.BackupID, VaultID: receipt.VaultID, Path: receipt.Path,
		SourceApplicationVersion: receipt.SourceApplicationVersion, SourceSchemaVersion: receipt.SourceSchemaVersion,
		CreatedAt: receipt.CreatedAt.UTC().Format(time.RFC3339Nano), EncryptedSizeBytes: receipt.EncryptedSizeBytes,
		CiphertextSHA256: receipt.CiphertextSHA256, DatabaseSizeBytes: receipt.DatabaseSizeBytes,
		ArtifactCount: receipt.ArtifactCount, EventCount: receipt.EventCount,
	}
}

func backupPreflightView(preflight vaultbackup.Preflight) VaultBackupPreflightView {
	return VaultBackupPreflightView{
		Schema: preflight.Schema, BackupID: preflight.BackupID, VaultID: preflight.VaultID, Path: preflight.Path,
		SourceApplicationVersion: preflight.SourceApplicationVersion, MinimumRestoreVersion: preflight.MinimumRestoreVersion,
		TargetApplicationVersion: preflight.TargetApplicationVersion,
		SourceSchemaVersion:      preflight.SourceSchemaVersion, TargetSchemaVersion: preflight.TargetSchemaVersion,
		MigrationRequired: preflight.MigrationRequired, CreatedAt: preflight.CreatedAt.UTC().Format(time.RFC3339Nano),
		VerifiedAt: preflight.VerifiedAt.UTC().Format(time.RFC3339Nano), EncryptedSizeBytes: preflight.EncryptedSizeBytes,
		CiphertextSHA256: preflight.CiphertextSHA256, DatabaseSizeBytes: preflight.DatabaseSizeBytes,
		ArtifactCount: preflight.ArtifactCount, EventCount: preflight.EventCount,
	}
}

func backupRestoreView(restored vaultbackup.Restore) VaultRestoreView {
	return VaultRestoreView{
		Schema: restored.Schema, State: string(restored.State), BackupID: restored.BackupID, VaultID: restored.VaultID,
		CiphertextSHA256: restored.CiphertextSHA256, SourceApplicationVersion: restored.SourceApplicationVersion,
		TargetApplicationVersion: restored.TargetApplicationVersion, SourceSchemaVersion: restored.SourceSchemaVersion,
		TargetSchemaVersion: restored.TargetSchemaVersion, RestoredAt: restored.RestoredAt.UTC().Format(time.RFC3339Nano),
		ArtifactCount: restored.ArtifactCount, EventCount: restored.EventCount,
	}
}
