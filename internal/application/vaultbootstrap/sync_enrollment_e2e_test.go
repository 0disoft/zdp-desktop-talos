package vaultbootstrap

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/dpapicatalog"
	"github.com/0disoft/zdp-desktop-talos/internal/adapters/localvaultdb"
	enrollmentapp "github.com/0disoft/zdp-desktop-talos/internal/application/syncenrollment"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
)

func TestEnrollmentEstablishesBidirectionalTrustAndReplaysAcrossIndependentVaults(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sourceCreator, _ := newEnrollmentTestCreator(t, filepath.Join(t.TempDir(), "source"))
	targetCreator, _ := newEnrollmentTestCreator(t, filepath.Join(t.TempDir(), "target"))
	sourceSession, err := sourceCreator.Create(ctx, CreateInput{RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer sourceSession.Close()
	workspace := filepath.Join(t.TempDir(), "repo")
	baseline := strings.Repeat("a", 40)
	createdTask, err := sourceSession.CreateTaskContract(ctx, CreateTaskContractInput{WorkspaceRoot: workspace, BaselineCommit: baseline, Goal: "establish a synced task", AllowedPaths: []string{"internal/**"}, ForbiddenActions: []string{"git.push"}, AcceptanceCriteria: []string{"the task replays on the enrolled device"}, VerificationCommands: []task.VerificationCommand{{RuleID: "go-test", Arguments: []string{"./..."}, WorkingDirectory: "."}}, Risk: task.RiskMedium, IdempotencyKey: "source-task"})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := sourceSession.CreateEnrollmentOffer(ctx, CreateEnrollmentOfferInput{ValidFor: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := targetCreator.AcceptEnrollmentOffer(ctx, AcceptEnrollmentOfferInput{Encoded: offer.Encoded, Secret: offer.Secret})
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Session.Close()
	if accepted.EnrollmentID != offer.EnrollmentID || accepted.SourceDeviceID == "" || accepted.TargetDeviceID == "" || accepted.SourceDeviceID == accepted.TargetDeviceID {
		t.Fatalf("accepted=%+v", accepted)
	}
	targetDevice, err := sourceSession.CompleteEnrollment(ctx, CompleteEnrollmentInput{EncodedAcceptance: accepted.EncodedAcceptance, Secret: offer.Secret})
	if err != nil {
		t.Fatal(err)
	}
	if targetDevice.DeviceID != accepted.TargetDeviceID {
		t.Fatalf("target device=%+v accepted=%+v", targetDevice, accepted)
	}
	if _, err := accepted.Session.database.GetSyncDevice(ctx, accepted.Session.Record.ID, accepted.SourceDeviceID); err != nil {
		t.Fatalf("target does not trust source: %v", err)
	}
	if _, err := sourceSession.database.GetSyncDevice(ctx, sourceSession.Record.ID, accepted.TargetDeviceID); err != nil {
		t.Fatalf("source does not trust target: %v", err)
	}

	sourcePack, err := sourceSession.ExportNextSyncPack(ctx, 64)
	if err != nil {
		t.Fatal(err)
	}
	toTarget, err := accepted.Session.ImportAndApplySyncPack(ctx, ImportSyncPackInput{Encoded: sourcePack.Encoded, DeviceID: accepted.SourceDeviceID, ReceivedAt: time.Now().UTC()})
	if err != nil || toTarget.Replay.Batch.AppliedCount < 1 {
		t.Fatalf("target replay=%+v error=%v", toTarget, err)
	}
	targetTask, err := accepted.Session.database.GetTask(ctx, createdTask.Task.ID)
	if err != nil || targetTask.CurrentRevision != 1 || targetTask.WorkspaceRoot != workspace {
		t.Fatalf("target task=%+v error=%v", targetTask, err)
	}
	if _, err := accepted.Session.ReviseTaskContract(ctx, ReviseTaskContractInput{TaskID: targetTask.ID, ExpectedRevision: 1, WorkspaceRoot: workspace, BaselineCommit: baseline, Goal: "return the synced revision", AllowedPaths: []string{"internal/**"}, ForbiddenActions: []string{"git.push"}, AcceptanceCriteria: []string{"the revision replays on the source device"}, VerificationCommands: []task.VerificationCommand{{RuleID: "go-test", Arguments: []string{"./..."}, WorkingDirectory: "."}}, Risk: task.RiskMedium, IdempotencyKey: "target-revision"}); err != nil {
		t.Fatal(err)
	}
	targetPack, err := accepted.Session.ExportNextSyncPack(ctx, 64)
	if err != nil {
		t.Fatal(err)
	}
	toSource, err := sourceSession.ImportAndApplySyncPack(ctx, ImportSyncPackInput{Encoded: targetPack.Encoded, DeviceID: accepted.TargetDeviceID, ReceivedAt: time.Now().UTC()})
	if err != nil || toSource.Replay.Batch.AppliedCount != 1 {
		t.Fatalf("source replay=%+v error=%v", toSource, err)
	}
	sourceTask, err := sourceSession.database.GetTask(ctx, createdTask.Task.ID)
	if err != nil || sourceTask.CurrentRevision != 2 {
		t.Fatalf("source task=%+v error=%v", sourceTask, err)
	}
	duplicate, err := sourceSession.ImportAndApplySyncPack(ctx, ImportSyncPackInput{Encoded: targetPack.Encoded, DeviceID: accepted.TargetDeviceID, ReceivedAt: time.Now().UTC()})
	if err != nil || !duplicate.Replayed {
		t.Fatalf("duplicate replay=%+v error=%v", duplicate, err)
	}

	replayedAcceptance, err := targetCreator.AcceptEnrollmentOffer(ctx, AcceptEnrollmentOfferInput{Encoded: offer.Encoded, Secret: offer.Secret})
	if err != nil {
		t.Fatal(err)
	}
	defer replayedAcceptance.Session.Close()
	if !replayedAcceptance.Replay || !bytes.Equal(replayedAcceptance.EncodedAcceptance, accepted.EncodedAcceptance) || replayedAcceptance.TargetDeviceID != accepted.TargetDeviceID {
		t.Fatalf("acceptance replay=%+v", replayedAcceptance)
	}
}

func TestEnrollmentRejectsWrongSecretAndConflictingVaultKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sourceCreator, _ := newEnrollmentTestCreator(t, filepath.Join(t.TempDir(), "source"))
	targetCreator, targetKeys := newEnrollmentTestCreator(t, filepath.Join(t.TempDir(), "target"))
	source, err := sourceCreator.Create(ctx, CreateInput{RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	offer, err := source.CreateEnrollmentOffer(ctx, CreateEnrollmentOfferInput{ValidFor: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	wrongSecret := "talos-enroll-v1." + strings.Repeat("A", 43)
	if _, err := targetCreator.AcceptEnrollmentOffer(ctx, AcceptEnrollmentOfferInput{Encoded: offer.Encoded, Secret: wrongSecret}); err == nil {
		t.Fatal("wrong enrollment secret was accepted")
	}
	decoded, _, err := enrollmentapp.NewCodec().OpenOffer(ctx, offer.Encoded, offer.Secret)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(decoded.VaultKey)
	if err := targetKeys.Put(ctx, keyvault.Reference{VaultID: decoded.VaultID, KeyID: VaultKeyID}, bytes.Repeat([]byte{0xff}, vaultKeyBytes)); err != nil {
		t.Fatal(err)
	}
	if _, err := targetCreator.AcceptEnrollmentOffer(ctx, AcceptEnrollmentOfferInput{Encoded: offer.Encoded, Secret: offer.Secret}); !errors.Is(err, ErrEnrollmentConflict) {
		t.Fatalf("conflicting key error=%v", err)
	}
}

func newEnrollmentTestCreator(t *testing.T, databaseRoot string) (*Creator, *enrollmentKeyStore) {
	t.Helper()
	keys := &enrollmentKeyStore{values: map[keyvault.Reference][]byte{}}
	catalog, err := dpapicatalog.New(keys)
	if err != nil {
		t.Fatal(err)
	}
	databases, err := localvaultdb.New(databaseRoot)
	if err != nil {
		t.Fatal(err)
	}
	creator, err := NewCreator(keys, databases, catalog)
	if err != nil {
		t.Fatal(err)
	}
	return creator, keys
}

type enrollmentKeyStore struct{ values map[keyvault.Reference][]byte }

func (s *enrollmentKeyStore) Put(_ context.Context, ref keyvault.Reference, value []byte) error {
	if _, exists := s.values[ref]; exists {
		return keyvault.ErrAlreadyExists
	}
	s.values[ref] = append([]byte(nil), value...)
	return nil
}

func (s *enrollmentKeyStore) Get(_ context.Context, ref keyvault.Reference) ([]byte, error) {
	value, exists := s.values[ref]
	if !exists {
		return nil, keyvault.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s *enrollmentKeyStore) Rotate(_ context.Context, ref keyvault.Reference, value []byte) error {
	if _, exists := s.values[ref]; !exists {
		return keyvault.ErrNotFound
	}
	s.values[ref] = append([]byte(nil), value...)
	return nil
}

func (s *enrollmentKeyStore) Delete(_ context.Context, ref keyvault.Reference) error {
	if _, exists := s.values[ref]; !exists {
		return keyvault.ErrNotFound
	}
	delete(s.values, ref)
	return nil
}
