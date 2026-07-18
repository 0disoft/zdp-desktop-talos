package syncenrollment

import (
	"strings"
	"testing"
	"time"
)

func TestTerminalEnrollmentRecordsPreserveOnlyRoleAppropriateEvidence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 18, 18, 0, 0, 0, time.UTC)
	base := Record{EnrollmentID: "enrollment", VaultID: "vault", OfferHash: strings.Repeat("a", 64), ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now.Add(time.Minute), CreatedEventID: "created", LastEventID: "last"}

	for _, state := range []State{StateCanceled, StateExpired} {
		issuer := base
		issuer.Role = RoleIssuer
		issuer.State = state
		if err := issuer.Validate(); err != nil {
			t.Fatalf("issuer %s: %v", state, err)
		}

		recipient := base
		recipient.Role = RoleRecipient
		recipient.State = state
		recipient.PeerDeviceID = "source-device"
		recipient.AcceptanceHash = strings.Repeat("b", 64)
		if err := recipient.Validate(); err != nil {
			t.Fatalf("recipient %s: %v", state, err)
		}
	}
}

func TestEnrollmentLifecycleGuards(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 18, 18, 0, 0, 0, time.UTC)
	record := Record{State: StateOffered, ExpiresAt: now.Add(time.Minute)}
	if !record.CanCancel() || record.CanExpireAt(now) || !record.CanExpireAt(now.Add(time.Minute)) {
		t.Fatalf("unexpected lifecycle guards for %+v", record)
	}
	record.State = StateCompleted
	if record.CanCancel() || record.CanExpireAt(now.Add(time.Hour)) {
		t.Fatalf("completed enrollment is not terminal: %+v", record)
	}
}
