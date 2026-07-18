package updatepreparation

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/releaseupdate"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/vault"
	"github.com/0disoft/zdp-desktop-talos/internal/id"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/updatejournal"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/updateverify"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultbackup"
)

var (
	ErrInvalidRequest      = errors.New("update preparation request is invalid")
	ErrVaultChanged        = errors.New("Vault changed during update preparation")
	ErrBackupMismatch      = errors.New("update backup preflight does not match its receipt")
	ErrPreparationMissing  = errors.New("update preparation is missing")
	ErrPreparationExpired  = errors.New("update preparation expired")
	ErrPreparationMismatch = errors.New("update preparation does not match current inputs")
	ErrJournalFailed       = errors.New("update preparation could not be recorded")
)

type Vault interface {
	CurrentVault() (vault.Record, error)
	CreateBackup(context.Context, string, string) (vaultbackup.Receipt, error)
	PreflightBackup(context.Context, string, string) (vaultbackup.Preflight, error)
}

type PrepareInput struct {
	ManifestPath          string
	SignaturePath         string
	ArtifactPath          string
	BackupDestination     string
	ExpectedVaultID       string
	ExpectedVaultRevision int
	Confirmation          string
}

type AuthorizeInput struct {
	ManifestPath          string
	SignaturePath         string
	ArtifactPath          string
	BackupPath            string
	ExpectedVaultID       string
	ExpectedVaultRevision int
}

type Policy struct {
	Channel      releaseupdate.Channel
	OS           string
	Architecture string
	Version      string
}

type Authorization struct {
	Preparation  releaseupdate.Preparation
	AuthorizedAt time.Time
}

type Service struct {
	verifier updateverify.Verifier
	journal  updatejournal.Store
	policy   Policy
	now      func() time.Time
	random   io.Reader
}

func New(verifier updateverify.Verifier, journal updatejournal.Store, policy Policy, now func() time.Time, randomSource io.Reader) (*Service, error) {
	if verifier == nil || journal == nil || !validPolicy(policy) || now == nil || randomSource == nil {
		return nil, ErrInvalidRequest
	}
	return &Service{verifier: verifier, journal: journal, policy: policy, now: now, random: randomSource}, nil
}

func NewDefault(verifier updateverify.Verifier, journal updatejournal.Store, policy Policy) (*Service, error) {
	return New(verifier, journal, policy, time.Now, rand.Reader)
}

func (s *Service) Prepare(ctx context.Context, current Vault, input PrepareInput) (releaseupdate.Preparation, error) {
	if s == nil || ctx == nil || current == nil || !validPrepareInput(input) {
		return releaseupdate.Preparation{}, ErrInvalidRequest
	}
	record, err := validateVault(current, input.ExpectedVaultID, input.ExpectedVaultRevision)
	if err != nil {
		return releaseupdate.Preparation{}, err
	}
	if input.Confirmation != record.ID {
		return releaseupdate.Preparation{}, ErrInvalidRequest
	}
	verified, err := s.verifier.VerifyFiles(ctx, s.verifyInput(input.ManifestPath, input.SignaturePath, input.ArtifactPath))
	if err != nil {
		return releaseupdate.Preparation{}, err
	}
	backupReceipt, err := current.CreateBackup(ctx, input.BackupDestination, s.policy.Version)
	if err != nil {
		return releaseupdate.Preparation{}, fmt.Errorf("create pre-update Vault backup: %w", err)
	}
	preflight, err := current.PreflightBackup(ctx, backupReceipt.Path, s.policy.Version)
	if err != nil {
		return releaseupdate.Preparation{}, fmt.Errorf("preflight pre-update Vault backup: %w", err)
	}
	if !matchingBackup(record, backupReceipt, preflight, s.policy.Version) {
		return releaseupdate.Preparation{}, ErrBackupMismatch
	}
	latest, err := current.CurrentVault()
	if err != nil || latest.ID != record.ID || latest.Revision != record.Revision {
		return releaseupdate.Preparation{}, ErrVaultChanged
	}
	preparedAt := s.now().UTC()
	if !verified.Manifest.ExpiresAt.After(preparedAt) {
		return releaseupdate.Preparation{}, ErrPreparationExpired
	}
	preparationID, err := id.UUIDv7(preparedAt, s.random)
	if err != nil {
		return releaseupdate.Preparation{}, fmt.Errorf("create update preparation ID: %w", err)
	}
	preparation := releaseupdate.Preparation{
		Schema: releaseupdate.PreparationSchema, PreparationID: preparationID, ReleaseID: verified.Manifest.ReleaseID,
		Channel: verified.Manifest.Channel, TargetVersion: verified.Manifest.Version, CurrentVersion: s.policy.Version,
		TargetOS: verified.Manifest.Target.OS, TargetArchitecture: verified.Manifest.Target.Architecture,
		ManifestSHA256: verified.ManifestSHA256, ArtifactName: verified.Manifest.Artifact.Name,
		ArtifactSHA256: verified.Manifest.Artifact.SHA256, SigningKeyID: verified.SigningKeyID,
		VaultID: record.ID, VaultRevision: record.Revision, BackupID: preflight.BackupID,
		BackupCiphertextSHA256: preflight.CiphertextSHA256, BackupSourceSchema: preflight.SourceSchemaVersion,
		BackupTargetSchema: preflight.TargetSchemaVersion, PreparedAt: preparedAt, ExpiresAt: verified.Manifest.ExpiresAt,
	}
	if err := releaseupdate.ValidatePreparation(preparation); err != nil {
		return releaseupdate.Preparation{}, fmt.Errorf("%w: %v", ErrJournalFailed, err)
	}
	if err := s.journal.Save(context.WithoutCancel(ctx), preparation); err != nil {
		return releaseupdate.Preparation{}, errors.Join(ErrJournalFailed, err)
	}
	return preparation, nil
}

func (s *Service) Authorize(ctx context.Context, current Vault, input AuthorizeInput) (Authorization, error) {
	if s == nil || ctx == nil || current == nil || !validAuthorizeInput(input) {
		return Authorization{}, ErrInvalidRequest
	}
	record, err := validateVault(current, input.ExpectedVaultID, input.ExpectedVaultRevision)
	if err != nil {
		return Authorization{}, err
	}
	verified, err := s.verifier.VerifyFiles(ctx, s.verifyInput(input.ManifestPath, input.SignaturePath, input.ArtifactPath))
	if err != nil {
		return Authorization{}, err
	}
	preparation, err := s.journal.Load(ctx, record.ID)
	if errors.Is(err, updatejournal.ErrNotFound) {
		return Authorization{}, ErrPreparationMissing
	}
	if err != nil {
		return Authorization{}, errors.Join(ErrJournalFailed, err)
	}
	now := s.now().UTC()
	if !preparation.ExpiresAt.After(now) {
		return Authorization{}, ErrPreparationExpired
	}
	preflight, err := current.PreflightBackup(ctx, input.BackupPath, s.policy.Version)
	if err != nil {
		return Authorization{}, fmt.Errorf("revalidate pre-update Vault backup: %w", err)
	}
	if !matchingAuthorization(preparation, verified, record, preflight, s.policy.Version) {
		return Authorization{}, ErrPreparationMismatch
	}
	latest, err := current.CurrentVault()
	if err != nil || latest.ID != record.ID || latest.Revision != record.Revision {
		return Authorization{}, ErrVaultChanged
	}
	return Authorization{Preparation: preparation, AuthorizedAt: now}, nil
}

func (s *Service) Invalidate(ctx context.Context, vaultID string) error {
	if s == nil || ctx == nil || strings.TrimSpace(vaultID) == "" {
		return ErrInvalidRequest
	}
	if err := s.journal.Delete(ctx, vaultID); errors.Is(err, updatejournal.ErrNotFound) {
		return nil
	} else if err != nil {
		return errors.Join(ErrJournalFailed, err)
	}
	return nil
}

func validateVault(current Vault, expectedID string, expectedRevision int) (vault.Record, error) {
	record, err := current.CurrentVault()
	if err != nil {
		return vault.Record{}, err
	}
	if record.ID != expectedID || record.Revision != expectedRevision || record.Status != vault.StatusActive {
		return vault.Record{}, ErrVaultChanged
	}
	return record, nil
}

func matchingBackup(record vault.Record, receipt vaultbackup.Receipt, preflight vaultbackup.Preflight, applicationVersion string) bool {
	return receipt.VaultID == record.ID && preflight.VaultID == record.ID && receipt.BackupID == preflight.BackupID &&
		receipt.CiphertextSHA256 == preflight.CiphertextSHA256 && receipt.SourceApplicationVersion == applicationVersion &&
		preflight.TargetApplicationVersion == applicationVersion && receipt.SourceSchemaVersion == preflight.SourceSchemaVersion &&
		receipt.ArtifactCount == preflight.ArtifactCount && receipt.EventCount == preflight.EventCount
}

func matchingAuthorization(preparation releaseupdate.Preparation, verified releaseupdate.VerifiedRelease, record vault.Record, preflight vaultbackup.Preflight, currentVersion string) bool {
	return preparation.ReleaseID == verified.Manifest.ReleaseID && preparation.Channel == verified.Manifest.Channel &&
		preparation.TargetVersion == verified.Manifest.Version && preparation.CurrentVersion == currentVersion &&
		preparation.TargetOS == verified.Manifest.Target.OS && preparation.TargetArchitecture == verified.Manifest.Target.Architecture &&
		preparation.ManifestSHA256 == verified.ManifestSHA256 && preparation.ArtifactName == verified.Manifest.Artifact.Name &&
		preparation.ArtifactSHA256 == verified.Manifest.Artifact.SHA256 && preparation.SigningKeyID == verified.SigningKeyID &&
		preparation.VaultID == record.ID && preparation.VaultRevision == record.Revision &&
		preparation.BackupID == preflight.BackupID && preparation.BackupCiphertextSHA256 == preflight.CiphertextSHA256 &&
		preparation.BackupSourceSchema == preflight.SourceSchemaVersion && preparation.BackupTargetSchema == preflight.TargetSchemaVersion
}

func (s *Service) verifyInput(manifestPath, signaturePath, artifactPath string) updateverify.VerifyFilesInput {
	return updateverify.VerifyFilesInput{ManifestPath: manifestPath, SignaturePath: signaturePath, ArtifactPath: artifactPath, ExpectedChannel: s.policy.Channel, ExpectedOS: s.policy.OS, ExpectedArchitecture: s.policy.Architecture, CurrentVersion: s.policy.Version}
}

func validPrepareInput(input PrepareInput) bool {
	return strings.TrimSpace(input.BackupDestination) != "" && input.Confirmation != "" && input.ExpectedVaultID != "" && input.ExpectedVaultRevision > 0 && validPaths(input.ManifestPath, input.SignaturePath, input.ArtifactPath)
}

func validAuthorizeInput(input AuthorizeInput) bool {
	return strings.TrimSpace(input.BackupPath) != "" && input.ExpectedVaultID != "" && input.ExpectedVaultRevision > 0 && validPaths(input.ManifestPath, input.SignaturePath, input.ArtifactPath)
}

func validPaths(manifestPath, signaturePath, artifactPath string) bool {
	return strings.TrimSpace(manifestPath) != "" && strings.TrimSpace(signaturePath) != "" && strings.TrimSpace(artifactPath) != ""
}

func validPolicy(policy Policy) bool {
	if !policy.Channel.Valid() || strings.TrimSpace(policy.OS) == "" || strings.TrimSpace(policy.Architecture) == "" {
		return false
	}
	_, err := releaseupdate.ParseVersion(policy.Version)
	return err == nil
}
