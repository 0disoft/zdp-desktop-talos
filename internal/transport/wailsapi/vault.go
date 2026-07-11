package wailsapi

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
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

type VaultService struct {
	mu                  sync.Mutex
	creator             *vaultbootstrap.Creator
	initializationError error
	session             *vaultbootstrap.Session
}

func NewVaultService(creator *vaultbootstrap.Creator, initializationError error) *VaultService {
	return &VaultService{creator: creator, initializationError: initializationError}
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

func (s *VaultService) Lock(correlationID string) VaultResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		status := s.statusLocked()
		return VaultResult{Vault: &status}
	}
	err := s.session.Close()
	s.session = nil
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
