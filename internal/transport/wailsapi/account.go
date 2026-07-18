package wailsapi

import (
	"context"
	"errors"
	"strings"

	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/accountlink"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/accountstore"
)

var errAccountLinkUpstreamNotReady = errors.New("ZDP product link upstream is not ready")

type AccountStatus struct {
	Mode          string `json:"mode"`
	State         string `json:"state"`
	LinkAvailable bool   `json:"link_available"`
	ReasonCode    string `json:"reason_code"`
	Revision      int    `json:"revision,omitempty"`
}

type AccountStatusResult struct {
	Account *AccountStatus `json:"account,omitempty"`
	Error   *TalosError    `json:"error,omitempty"`
}

type AccountUnlinkRequest struct {
	ExpectedRevision int    `json:"expected_revision"`
	RequestID        string `json:"request_id"`
	CorrelationID    string `json:"correlation_id"`
}

type AccountService struct {
	vault *VaultService
}

func NewAccountService(vault *VaultService) *AccountService {
	return &AccountService{vault: vault}
}

func (s *AccountService) Status(correlationID string) AccountStatusResult {
	correlationID = normalizeCorrelationID(correlationID)
	if s == nil || s.vault == nil {
		return accountStatusError(vaultbootstrap.ErrNotOpen, correlationID)
	}
	record, found, err := s.currentRecord()
	if errors.Is(err, vaultbootstrap.ErrNotOpen) {
		status := accountStatus("vault_locked", 0)
		status.ReasonCode = "VAULT_NOT_OPEN"
		return AccountStatusResult{Account: &status}
	}
	if err != nil {
		return accountStatusError(err, correlationID)
	}
	if !found {
		status := accountStatus("unlinked", 0)
		return AccountStatusResult{Account: &status}
	}
	status := accountStatus(string(record.State), record.Revision)
	return AccountStatusResult{Account: &status}
}

func (s *AccountService) Unlink(request AccountUnlinkRequest) AccountStatusResult {
	correlationID := normalizeCorrelationID(request.CorrelationID)
	requestID := strings.TrimSpace(request.RequestID)
	if s == nil || s.vault == nil || request.ExpectedRevision < 1 || normalizeCorrelationID(requestID) == "" {
		return accountStatusError(accountstore.ErrInvalidCommand, correlationID)
	}
	record, err := s.unlink(request.ExpectedRevision, "account-unlink:"+requestID)
	if err != nil {
		return accountStatusError(err, correlationID)
	}
	status := accountStatus(string(record.State), record.Revision)
	return AccountStatusResult{Account: &status}
}

func accountStatus(state string, revision int) AccountStatus {
	return AccountStatus{Mode: "local_only", State: state, LinkAvailable: false, ReasonCode: "PRODUCT_LINK_UPSTREAM_NOT_READY", Revision: revision}
}

func accountStatusError(err error, correlationID string) AccountStatusResult {
	mapped := MapError(err, correlationID)
	return AccountStatusResult{Error: &mapped}
}

func (s *AccountService) currentRecord() (accountlink.Record, bool, error) {
	s.vault.mu.Lock()
	defer s.vault.mu.Unlock()
	if s.vault.session == nil {
		return accountlink.Record{}, false, vaultbootstrap.ErrNotOpen
	}
	database, err := s.vault.session.AccountDatabase()
	if err != nil {
		return accountlink.Record{}, false, err
	}
	record, err := database.GetAccountLink(context.Background(), s.vault.session.Record.ID)
	if errors.Is(err, accountstore.ErrNotFound) {
		return accountlink.Record{}, false, nil
	}
	return record, err == nil, err
}

func (s *AccountService) unlink(expectedRevision int, idempotencyKey string) (accountlink.Record, error) {
	s.vault.mu.Lock()
	defer s.vault.mu.Unlock()
	if s.vault.session == nil {
		return accountlink.Record{}, vaultbootstrap.ErrNotOpen
	}
	database, err := s.vault.session.AccountDatabase()
	if err != nil {
		return accountlink.Record{}, err
	}
	return database.UnlinkAccount(context.Background(), accountstore.UnlinkInput{VaultID: s.vault.session.Record.ID, ExpectedRevision: expectedRevision, IdempotencyKey: idempotencyKey})
}
