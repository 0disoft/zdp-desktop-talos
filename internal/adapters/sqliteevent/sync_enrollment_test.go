package sqliteevent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/syncenrollment"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/enrollmentstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
)

func TestEnrollmentOfferCompletionIsDurableAndIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 14, 0, 0, 0, time.UTC)
	store := openTestStore(t, filepath.Join(t.TempDir(), "enrollment.db"))
	defer store.Close()
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: "vault-enrollment", RetentionDays: 30, OccurredAt: now, IdempotencyKey: "vault"}); err != nil {
		t.Fatal(err)
	}
	input := enrollmentstore.RecordOfferInput{EnrollmentID: "enrollment-1", VaultID: "vault-enrollment", OfferHash: strings.Repeat("a", 64), ExpiresAt: now.Add(time.Hour), OccurredAt: now.Add(time.Minute)}
	created, replay, err := store.RecordEnrollmentOffer(ctx, input)
	if err != nil || replay || created.State != syncenrollment.StateOffered {
		t.Fatalf("created=%+v replay=%v error=%v", created, replay, err)
	}
	replayed, replay, err := store.RecordEnrollmentOffer(ctx, input)
	if err != nil || !replay || replayed.LastEventID != created.LastEventID {
		t.Fatalf("replayed=%+v replay=%v error=%v", replayed, replay, err)
	}
	completedInput := enrollmentstore.CompleteInput{EnrollmentID: input.EnrollmentID, VaultID: input.VaultID, TargetDeviceID: "device-target", OfferHash: input.OfferHash, AcceptanceHash: strings.Repeat("b", 64), OccurredAt: now.Add(2 * time.Minute)}
	completed, replay, err := store.CompleteEnrollment(ctx, completedInput)
	if err != nil || replay || completed.State != syncenrollment.StateCompleted || completed.PeerDeviceID != "device-target" {
		t.Fatalf("completed=%+v replay=%v error=%v", completed, replay, err)
	}
	completedAgain, replay, err := store.CompleteEnrollment(ctx, completedInput)
	if err != nil || !replay || completedAgain.LastEventID != completed.LastEventID {
		t.Fatalf("completed again=%+v replay=%v error=%v", completedAgain, replay, err)
	}
	changed := completedInput
	changed.TargetDeviceID = "device-other"
	if _, _, err := store.CompleteEnrollment(ctx, changed); !errors.Is(err, enrollmentstore.ErrConflict) {
		t.Fatalf("changed completion error=%v", err)
	}
}

func TestEnrollmentAcceptanceResponseIsEncryptedRecoverableAndTamperEvident(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 15, 0, 0, 0, time.UTC)
	store := openTestStore(t, filepath.Join(t.TempDir(), "acceptance.db"))
	defer store.Close()
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: "vault-recipient", RetentionDays: 30, OccurredAt: now, IdempotencyKey: "vault"}); err != nil {
		t.Fatal(err)
	}
	response := []byte(`{"schema":"talos.sync-enrollment-package/1","marker":"acceptance-private-marker"}`)
	hash := sha256.Sum256(response)
	input := enrollmentstore.RecordAcceptanceInput{EnrollmentID: "enrollment-2", VaultID: "vault-recipient", SourceDeviceID: "device-source", OfferHash: strings.Repeat("c", 64), AcceptanceHash: hex.EncodeToString(hash[:]), EncodedAcceptance: response, ExpiresAt: now.Add(time.Hour), OccurredAt: now.Add(time.Minute)}
	record, stored, replay, err := store.RecordEnrollmentAcceptance(ctx, input)
	if err != nil || replay || record.State != syncenrollment.StateAccepted || !bytes.Equal(stored, response) {
		t.Fatalf("record=%+v replay=%v stored=%q error=%v", record, replay, stored, err)
	}
	var encrypted []byte
	if err := store.db.QueryRow(`SELECT acceptance_envelope FROM sync_enrollments WHERE enrollment_id = ?`, input.EnrollmentID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte("acceptance-private-marker")) {
		t.Fatal("acceptance response was stored as plaintext")
	}
	loaded, recovered, err := store.GetEnrollment(ctx, input.VaultID, input.EnrollmentID)
	if err != nil || loaded.AcceptanceHash != input.AcceptanceHash || !bytes.Equal(recovered, response) {
		t.Fatalf("loaded=%+v recovered=%q error=%v", loaded, recovered, err)
	}
	encrypted[len(encrypted)-1] ^= 1
	if _, err := store.db.Exec(`UPDATE sync_enrollments SET acceptance_envelope = ? WHERE enrollment_id = ?`, encrypted, input.EnrollmentID); err != nil {
		t.Fatal(err)
	}
	if _, recovered, err := store.GetEnrollment(ctx, input.VaultID, input.EnrollmentID); err == nil {
		clear(recovered)
		t.Fatal("tampered acceptance envelope was accepted")
	}
}

func TestEnrollmentCancellationIsTerminalIdempotentAndClearsRecipientResponse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 16, 0, 0, 0, time.UTC)
	store := openTestStore(t, filepath.Join(t.TempDir(), "cancel.db"))
	defer store.Close()
	for _, vaultID := range []string{"vault-issuer-cancel", "vault-recipient-cancel"} {
		if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: vaultID, RetentionDays: 30, OccurredAt: now, IdempotencyKey: "vault:" + vaultID}); err != nil {
			t.Fatal(err)
		}
	}

	offer := enrollmentstore.RecordOfferInput{EnrollmentID: "cancel-offer", VaultID: "vault-issuer-cancel", OfferHash: strings.Repeat("d", 64), ExpiresAt: now.Add(time.Hour), OccurredAt: now.Add(time.Minute)}
	if _, _, err := store.RecordEnrollmentOffer(ctx, offer); err != nil {
		t.Fatal(err)
	}
	canceled, replay, err := store.CancelEnrollment(ctx, enrollmentstore.CancelInput{EnrollmentID: offer.EnrollmentID, VaultID: offer.VaultID, OccurredAt: now.Add(2 * time.Minute)})
	if err != nil || replay || canceled.State != syncenrollment.StateCanceled {
		t.Fatalf("canceled=%+v replay=%v error=%v", canceled, replay, err)
	}
	replayed, replay, err := store.CancelEnrollment(ctx, enrollmentstore.CancelInput{EnrollmentID: offer.EnrollmentID, VaultID: offer.VaultID, OccurredAt: now.Add(3 * time.Minute)})
	if err != nil || !replay || replayed.LastEventID != canceled.LastEventID {
		t.Fatalf("replayed=%+v replay=%v error=%v", replayed, replay, err)
	}
	if _, _, err := store.CompleteEnrollment(ctx, enrollmentstore.CompleteInput{EnrollmentID: offer.EnrollmentID, VaultID: offer.VaultID, TargetDeviceID: "late-target", OfferHash: offer.OfferHash, AcceptanceHash: strings.Repeat("e", 64), OccurredAt: now.Add(4 * time.Minute)}); !errors.Is(err, enrollmentstore.ErrConflict) {
		t.Fatalf("late completion error=%v", err)
	}

	response := []byte(`{"schema":"talos.sync-enrollment-package/1","marker":"discard-after-cancel"}`)
	hash := sha256.Sum256(response)
	acceptance := enrollmentstore.RecordAcceptanceInput{EnrollmentID: "cancel-acceptance", VaultID: "vault-recipient-cancel", SourceDeviceID: "source", OfferHash: strings.Repeat("f", 64), AcceptanceHash: hex.EncodeToString(hash[:]), EncodedAcceptance: response, ExpiresAt: now.Add(time.Hour), OccurredAt: now.Add(time.Minute)}
	if _, _, _, err := store.RecordEnrollmentAcceptance(ctx, acceptance); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CancelEnrollment(ctx, enrollmentstore.CancelInput{EnrollmentID: acceptance.EnrollmentID, VaultID: acceptance.VaultID, OccurredAt: now.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	loaded, recovered, err := store.GetEnrollment(ctx, acceptance.VaultID, acceptance.EnrollmentID)
	if err != nil || loaded.State != syncenrollment.StateCanceled || len(recovered) != 0 {
		clear(recovered)
		t.Fatalf("loaded=%+v recovered=%q error=%v", loaded, recovered, err)
	}
	var envelope []byte
	if err := store.db.QueryRow(`SELECT acceptance_envelope FROM sync_enrollments WHERE enrollment_id = ?`, acceptance.EnrollmentID).Scan(&envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope) != 0 {
		t.Fatal("canceled recipient retained its acceptance envelope")
	}
}

func TestExpiredEnrollmentsReconcileAfterRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "expiry.db")
	store := openTestStore(t, path)
	now := time.Now().UTC()
	vaultID := "vault-expiry"
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: vaultID, RetentionDays: 30, OccurredAt: now.Add(-time.Hour), IdempotencyKey: "vault"}); err != nil {
		t.Fatal(err)
	}
	input := enrollmentstore.RecordOfferInput{EnrollmentID: "expired-offer", VaultID: vaultID, OfferHash: strings.Repeat("1", 64), ExpiresAt: now.Add(-time.Minute), OccurredAt: now.Add(-2 * time.Minute)}
	if _, _, err := store.RecordEnrollmentOffer(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openTestStore(t, path)
	defer reopened.Close()
	loaded, response, err := reopened.GetEnrollment(ctx, vaultID, input.EnrollmentID)
	if err != nil || loaded.State != syncenrollment.StateExpired || len(response) != 0 || loaded.LastEventID == loaded.CreatedEventID {
		clear(response)
		t.Fatalf("loaded=%+v response=%q error=%v", loaded, response, err)
	}
	expired, err := reopened.ExpireEnrollments(ctx, vaultID, now.Add(time.Hour))
	if err != nil || len(expired) != 0 {
		t.Fatalf("second expiry sweep=%+v error=%v", expired, err)
	}
}
