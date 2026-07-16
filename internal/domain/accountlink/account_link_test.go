package accountlink

import (
	"testing"
	"time"
)

func TestLinkedAndUnlinkedRecordsKeepAccountDataOutOfInactiveState(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 4, 0, 0, 0, time.UTC)
	linked := Record{MembershipID: "membership-1", VaultID: "vault-1", State: StateLinked, Revision: 1, LinkReceiptRef: "link_01JTEST", SubjectRef: "usr_01JTEST", WorkspaceRef: "wks_01JTEST", ConsentReceiptRef: "consent_01JTEST", CreatedAt: now, LinkedAt: now, UpdatedAt: now, LastVerifiedAt: now, LastEventID: "event-1"}
	if err := linked.Validate(); err != nil {
		t.Fatal(err)
	}
	unlinked := linked
	unlinked.State, unlinked.Revision, unlinked.LinkReceiptRef, unlinked.SubjectRef, unlinked.WorkspaceRef, unlinked.ConsentReceiptRef = StateUnlinked, 2, "", "", "", ""
	unlinked.LinkedAt, unlinked.LastVerifiedAt, unlinked.UpdatedAt, unlinked.LastEventID = time.Time{}, time.Time{}, now.Add(time.Second), "event-2"
	if err := unlinked.Validate(); err != nil {
		t.Fatal(err)
	}
	unlinked.SubjectRef = "usr_must_not_remain"
	if err := unlinked.Validate(); err == nil {
		t.Fatal("unlinked record retained an active subject reference")
	}
}

func TestVerifiedIdentityRejectsContactValuesAndWhitespace(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	for _, subject := range []string{"", "person@example.com", "user with space", "사용자"} {
		identity := VerifiedIdentity{LinkReceiptRef: "link-1", SubjectRef: subject, ConsentReceiptRef: "consent-1", VerifiedAt: now}
		if err := identity.Validate(); err == nil {
			t.Fatalf("subject %q was accepted", subject)
		}
	}
}
