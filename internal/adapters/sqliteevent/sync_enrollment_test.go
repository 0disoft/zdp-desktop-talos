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
