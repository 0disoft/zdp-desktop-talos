package accountlink

import (
	"context"
	"errors"
	"testing"
	"time"

	accountdomain "github.com/0disoft/zdp-desktop-talos/internal/domain/accountlink"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/accountidentity"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/accountstore"
	fakeidentity "github.com/0disoft/zdp-desktop-talos/internal/testsupport/accountidentity"
)

func TestLinkConsumesVerifiedChallengeWithoutReceivingCredentials(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 7, 0, 0, 0, time.UTC)
	identity := accountdomain.VerifiedIdentity{LinkReceiptRef: "link_01JACCOUNT", SubjectRef: "usr_01JACCOUNT", WorkspaceRef: "wks_01JACCOUNT", ConsentReceiptRef: "consent_01JACCOUNT", VerifiedAt: now}
	verifier := fakeidentity.New(map[string]accountdomain.VerifiedIdentity{"link-1": identity})
	store := &linkStore{}
	service, err := New(store, verifier)
	if err != nil {
		t.Fatal(err)
	}
	request := LinkRequest{VaultID: "vault-1", ProductRef: "talos", ClientInstanceRef: "instance-1", CorrelationID: "correlation-1", RequestedScopeRefs: []string{"account.link"}, ExpectedRevision: 0, IdempotencyKey: "link-1"}
	result, err := service.Link(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.SubjectRef != identity.SubjectRef || store.linkInput.Identity != identity || store.linkInput.VaultID != "vault-1" {
		t.Fatalf("result=%+v input=%+v", result, store.linkInput)
	}
	if _, err := service.Link(context.Background(), request); err != nil {
		t.Fatalf("same command retry failed: %v", err)
	}
	request.CorrelationID = "correlation-2"
	if _, err := service.Link(context.Background(), request); !errors.Is(err, accountidentity.ErrConsumed) {
		t.Fatalf("reused challenge error=%v", err)
	}
}

func TestUnavailableAccountVerificationDoesNotMutateLocalMembership(t *testing.T) {
	t.Parallel()
	store := &linkStore{}
	service, err := New(store, fakeidentity.Unavailable(accountidentity.ErrUnavailable))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Link(context.Background(), LinkRequest{VaultID: "vault-1", ProductRef: "talos", ClientInstanceRef: "instance-1", CorrelationID: "correlation-1", RequestedScopeRefs: []string{"account.link"}, ExpectedRevision: 0, IdempotencyKey: "link-1"})
	if !errors.Is(err, accountidentity.ErrUnavailable) || store.linkCalls != 0 {
		t.Fatalf("error=%v linkCalls=%d", err, store.linkCalls)
	}
}

func TestLinkRejectsUnboundedOrControlBearingChallengeIdentifiers(t *testing.T) {
	t.Parallel()
	store := &linkStore{}
	service, err := New(store, fakeidentity.New(nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, productRef := range []string{"talos\nforged", "talos with space", string(make([]byte, 129))} {
		if _, err := service.Link(context.Background(), LinkRequest{VaultID: "vault-1", ProductRef: productRef, ClientInstanceRef: "instance-1", CorrelationID: "correlation-1", RequestedScopeRefs: []string{"account.link"}, IdempotencyKey: "link-1"}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("product ref %q error=%v", productRef, err)
		}
	}
	if store.linkCalls != 0 {
		t.Fatalf("invalid challenges reached persistence: %d", store.linkCalls)
	}
}

type linkStore struct {
	linkInput accountstore.LinkInput
	linkCalls int
}

func (s *linkStore) LinkAccount(_ context.Context, input accountstore.LinkInput) (accountdomain.Record, error) {
	s.linkCalls++
	s.linkInput = input
	return accountdomain.Record{MembershipID: "membership-1", VaultID: input.VaultID, State: accountdomain.StateLinked, Revision: input.ExpectedRevision + 1, LinkReceiptRef: input.Identity.LinkReceiptRef, SubjectRef: input.Identity.SubjectRef, WorkspaceRef: input.Identity.WorkspaceRef, ConsentReceiptRef: input.Identity.ConsentReceiptRef, CreatedAt: input.Identity.VerifiedAt, LinkedAt: input.Identity.VerifiedAt, UpdatedAt: input.Identity.VerifiedAt, LastVerifiedAt: input.Identity.VerifiedAt, LastEventID: "event-1"}, nil
}
func (*linkStore) UnlinkAccount(context.Context, accountstore.UnlinkInput) (accountdomain.Record, error) {
	return accountdomain.Record{}, accountstore.ErrNotFound
}
func (*linkStore) GetAccountLink(context.Context, string) (accountdomain.Record, error) {
	return accountdomain.Record{}, accountstore.ErrNotFound
}
