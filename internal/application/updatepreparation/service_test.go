package updatepreparation

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/releaseupdate"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/vault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/updatejournal"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/updateverify"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultbackup"
)

const (
	vaultID   = "019f5f1a-2000-7000-8000-000000000001"
	releaseID = "019f5f1a-2000-7000-8000-000000000002"
	backupID  = "019f5f1a-2000-7000-8000-000000000003"
)

func TestPrepareVerifiesReleaseBeforeCreatingAndPreflightingBackup(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 19, 13, 0, 0, 0, time.UTC)
	verifier := &fakeVerifier{release: verifiedRelease(now)}
	journal := &memoryJournal{}
	service, err := New(verifier, journal, testPolicy(), func() time.Time { return now }, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	current := newFakeVault(now)
	preparation, err := service.Prepare(context.Background(), current, prepareInput())
	if err != nil {
		t.Fatal(err)
	}
	if verifier.calls != 1 || current.createCalls != 1 || current.preflightCalls != 1 || journal.saved.PreparationID == "" {
		t.Fatalf("verification/backup/journal calls = %d/%d/%d, preparation=%+v", verifier.calls, current.createCalls, current.preflightCalls, journal.saved)
	}
	if verifier.lastInput.ExpectedChannel != releaseupdate.ChannelBeta || verifier.lastInput.ExpectedOS != "windows" || verifier.lastInput.ExpectedArchitecture != "amd64" || verifier.lastInput.CurrentVersion != "0.32.0" {
		t.Fatalf("verifier policy was not fixed by assembly: %+v", verifier.lastInput)
	}
	if preparation.VaultID != vaultID || preparation.VaultRevision != 2 || preparation.BackupID != backupID || preparation.ManifestSHA256 != hash("a") {
		t.Fatalf("unexpected preparation: %+v", preparation)
	}

	authorization, err := service.Authorize(context.Background(), current, authorizeInput())
	if err != nil {
		t.Fatal(err)
	}
	if authorization.Preparation.PreparationID != preparation.PreparationID || !authorization.AuthorizedAt.Equal(now) || current.preflightCalls != 2 {
		t.Fatalf("unexpected authorization: %+v; preflights=%d", authorization, current.preflightCalls)
	}
}

func TestPrepareFailsBeforeBackupWhenReleaseVerificationFails(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 19, 13, 0, 0, 0, time.UTC)
	verifier := &fakeVerifier{err: updateverify.ErrSignatureInvalid}
	service, _ := New(verifier, &memoryJournal{}, testPolicy(), func() time.Time { return now }, rand.Reader)
	current := newFakeVault(now)
	_, err := service.Prepare(context.Background(), current, prepareInput())
	if !errors.Is(err, updateverify.ErrSignatureInvalid) || current.createCalls != 0 || current.preflightCalls != 0 {
		t.Fatalf("Prepare() error=%v, backup calls=%d/%d", err, current.createCalls, current.preflightCalls)
	}
}

func TestAuthorizeFailsClosedForMissingStaleOrChangedEvidence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 19, 13, 0, 0, 0, time.UTC)
	current := newFakeVault(now)
	service, _ := New(&fakeVerifier{release: verifiedRelease(now)}, &memoryJournal{}, testPolicy(), func() time.Time { return now }, rand.Reader)
	if _, err := service.Authorize(context.Background(), current, authorizeInput()); !errors.Is(err, ErrPreparationMissing) {
		t.Fatalf("missing preparation error = %v", err)
	}

	journal := &memoryJournal{saved: prepared(now)}
	service, _ = New(&fakeVerifier{release: verifiedRelease(now)}, journal, testPolicy(), func() time.Time { return now.Add(3 * time.Hour) }, rand.Reader)
	if _, err := service.Authorize(context.Background(), current, authorizeInput()); !errors.Is(err, ErrPreparationExpired) {
		t.Fatalf("expired preparation error = %v", err)
	}

	journal.saved = prepared(now)
	current.record.Revision++
	if _, err := service.Authorize(context.Background(), current, authorizeInput()); !errors.Is(err, ErrVaultChanged) {
		t.Fatalf("changed Vault error = %v", err)
	}
}

func TestPrepareReturnsJournalFailureAfterLeavingVerifiedBackup(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 19, 13, 0, 0, 0, time.UTC)
	journal := &memoryJournal{saveErr: errors.New("protected store unavailable")}
	service, _ := New(&fakeVerifier{release: verifiedRelease(now)}, journal, testPolicy(), func() time.Time { return now }, rand.Reader)
	current := newFakeVault(now)
	_, err := service.Prepare(context.Background(), current, prepareInput())
	if !errors.Is(err, ErrJournalFailed) || current.createCalls != 1 || current.preflightCalls != 1 {
		t.Fatalf("Prepare() error=%v, backup calls=%d/%d", err, current.createCalls, current.preflightCalls)
	}
}

func prepareInput() PrepareInput {
	return PrepareInput{
		ManifestPath: "manifest.json", SignaturePath: "manifest.sig", ArtifactPath: "talos-setup.exe", BackupDestination: "backup.talos-backup",
		ExpectedVaultID: vaultID, ExpectedVaultRevision: 2, Confirmation: vaultID,
	}
}

func authorizeInput() AuthorizeInput {
	return AuthorizeInput{
		ManifestPath: "manifest.json", SignaturePath: "manifest.sig", ArtifactPath: "talos-setup.exe", BackupPath: "backup.talos-backup",
		ExpectedVaultID: vaultID, ExpectedVaultRevision: 2,
	}
}

func testPolicy() Policy {
	return Policy{Channel: releaseupdate.ChannelBeta, OS: "windows", Architecture: "amd64", Version: "0.32.0"}
}

func verifiedRelease(now time.Time) releaseupdate.VerifiedRelease {
	return releaseupdate.VerifiedRelease{
		Manifest: releaseupdate.Manifest{
			Schema: releaseupdate.ManifestSchema, ReleaseID: releaseID, Channel: releaseupdate.ChannelBeta, Version: "0.33.0", MinimumCurrentVersion: "0.32.0",
			PublishedAt: now.Add(-time.Hour), ExpiresAt: now.Add(2 * time.Hour), Target: releaseupdate.Target{OS: "windows", Architecture: "amd64"},
			Artifact: releaseupdate.Artifact{Name: "talos-setup.exe", SizeBytes: 1234, SHA256: hash("b")},
		},
		ManifestSHA256: hash("a"), SigningKeyID: hash("c"), VerifiedAt: now,
	}
}

func prepared(now time.Time) releaseupdate.Preparation {
	return releaseupdate.Preparation{
		Schema: releaseupdate.PreparationSchema, PreparationID: "019f5f1a-2000-7000-8000-000000000004", ReleaseID: releaseID,
		Channel: releaseupdate.ChannelBeta, TargetVersion: "0.33.0", CurrentVersion: "0.32.0", TargetOS: "windows", TargetArchitecture: "amd64",
		ManifestSHA256: hash("a"), ArtifactName: "talos-setup.exe", ArtifactSHA256: hash("b"), SigningKeyID: hash("c"),
		VaultID: vaultID, VaultRevision: 2, BackupID: backupID, BackupCiphertextSHA256: hash("d"), BackupSourceSchema: 23, BackupTargetSchema: 23,
		PreparedAt: now, ExpiresAt: now.Add(2 * time.Hour),
	}
}

func newFakeVault(now time.Time) *fakeVault {
	return &fakeVault{
		record:    vault.Record{ID: vaultID, Revision: 2, Status: vault.StatusActive, RetentionDays: 30, CreatedAt: now.Add(-time.Hour), UpdatedAt: now, LastEventID: "event-1"},
		receipt:   vaultbackup.Receipt{Schema: vaultbackup.ReceiptSchema, BackupID: backupID, VaultID: vaultID, Path: "backup.talos-backup", SourceApplicationVersion: "0.32.0", SourceSchemaVersion: 23, CreatedAt: now, CiphertextSHA256: hash("d"), ArtifactCount: 2, EventCount: 3},
		preflight: vaultbackup.Preflight{Schema: vaultbackup.PreflightSchema, BackupID: backupID, VaultID: vaultID, Path: "backup.talos-backup", SourceApplicationVersion: "0.32.0", TargetApplicationVersion: "0.32.0", SourceSchemaVersion: 23, TargetSchemaVersion: 23, CiphertextSHA256: hash("d"), ArtifactCount: 2, EventCount: 3},
	}
}

func hash(character string) string {
	value := ""
	for len(value) < 64 {
		value += character
	}
	return value[:64]
}

type fakeVerifier struct {
	release   releaseupdate.VerifiedRelease
	err       error
	calls     int
	lastInput updateverify.VerifyFilesInput
}

func (v *fakeVerifier) VerifyFiles(_ context.Context, input updateverify.VerifyFilesInput) (releaseupdate.VerifiedRelease, error) {
	v.calls++
	v.lastInput = input
	return v.release, v.err
}

type memoryJournal struct {
	saved   releaseupdate.Preparation
	saveErr error
}

func (j *memoryJournal) Save(_ context.Context, preparation releaseupdate.Preparation) error {
	if j.saveErr != nil {
		return j.saveErr
	}
	j.saved = preparation
	return nil
}

func (j *memoryJournal) Load(_ context.Context, vaultID string) (releaseupdate.Preparation, error) {
	if j.saved.VaultID != vaultID {
		return releaseupdate.Preparation{}, updatejournal.ErrNotFound
	}
	return j.saved, nil
}

func (j *memoryJournal) Delete(_ context.Context, vaultID string) error {
	if j.saved.VaultID != vaultID {
		return updatejournal.ErrNotFound
	}
	j.saved = releaseupdate.Preparation{}
	return nil
}

type fakeVault struct {
	record         vault.Record
	receipt        vaultbackup.Receipt
	preflight      vaultbackup.Preflight
	createCalls    int
	preflightCalls int
}

func (v *fakeVault) CurrentVault() (vault.Record, error) { return v.record, nil }

func (v *fakeVault) CreateBackup(context.Context, string, string) (vaultbackup.Receipt, error) {
	v.createCalls++
	return v.receipt, nil
}

func (v *fakeVault) PreflightBackup(context.Context, string, string) (vaultbackup.Preflight, error) {
	v.preflightCalls++
	return v.preflight, nil
}
