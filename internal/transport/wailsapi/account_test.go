package wailsapi

import (
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/accountlink"
)

func TestAccountServiceStaysLocalOnlyAndSupportsOfflineUnlink(t *testing.T) {
	t.Parallel()
	database := &serviceDatabase{}
	creator, err := vaultbootstrap.NewCreator(&serviceKeyStore{}, &serviceDatabaseFactory{database: database}, &serviceCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	vaultService := NewVaultService(creator, nil)
	service := NewAccountService(vaultService)
	locked := service.Status("account-status-locked")
	if locked.Error != nil || locked.Account == nil || locked.Account.State != "vault_locked" || locked.Account.LinkAvailable || locked.Account.ReasonCode != "VAULT_NOT_OPEN" {
		t.Fatalf("locked=%+v", locked)
	}
	created := vaultService.Create(30, "create-account-vault")
	if created.Error != nil || created.Vault == nil {
		t.Fatalf("created=%+v", created)
	}
	unlinked := service.Status("account-status-unlinked")
	if unlinked.Error != nil || unlinked.Account == nil || unlinked.Account.State != "unlinked" || unlinked.Account.Mode != "local_only" || unlinked.Account.LinkAvailable || unlinked.Account.ReasonCode != "PRODUCT_LINK_UPSTREAM_NOT_READY" {
		t.Fatalf("unlinked=%+v", unlinked)
	}
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	database.accountRecord = accountlink.Record{MembershipID: "membership-local", VaultID: created.Vault.VaultID, State: accountlink.StateLinked, Revision: 3, LinkReceiptRef: "link-private", SubjectRef: "subject-private", ConsentReceiptRef: "consent-private", CreatedAt: now, LinkedAt: now, UpdatedAt: now, LastVerifiedAt: now, LastEventID: "account-event"}
	linked := service.Status("account-status-linked")
	if linked.Error != nil || linked.Account == nil || linked.Account.State != "linked" || linked.Account.Revision != 3 || linked.Account.LinkAvailable {
		t.Fatalf("linked=%+v", linked)
	}
	result := service.Unlink(AccountUnlinkRequest{ExpectedRevision: 3, RequestID: "unlink-1", CorrelationID: "account-unlink"})
	if result.Error != nil || result.Account == nil || result.Account.State != "unlinked" || result.Account.Revision != 4 || database.accountUnlinkInput.IdempotencyKey != "account-unlink:unlink-1" {
		t.Fatalf("result=%+v input=%+v", result, database.accountUnlinkInput)
	}
}
