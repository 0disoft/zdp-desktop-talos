package vaultbootstrap

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/dpapicatalog"
	"github.com/0disoft/zdp-desktop-talos/internal/adapters/gitcli"
	"github.com/0disoft/zdp-desktop-talos/internal/adapters/localvaultdb"
	"github.com/0disoft/zdp-desktop-talos/internal/application/contextassembly"
	enrollmentapp "github.com/0disoft/zdp-desktop-talos/internal/application/syncenrollment"
	"github.com/0disoft/zdp-desktop-talos/internal/application/syncidentity"
	"github.com/0disoft/zdp-desktop-talos/internal/application/workspaceremap"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/syncstate"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorycontext"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorystore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/syncstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/workspacestore"
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
	workspace, targetWorkspace, baseline := enrollmentWorkspacePair(t)
	createdTask, err := sourceSession.CreateTaskContract(ctx, CreateTaskContractInput{WorkspaceRoot: workspace, BaselineCommit: baseline, Goal: "establish a synced task", AllowedPaths: []string{"internal/**"}, ForbiddenActions: []string{"git.push"}, AcceptanceCriteria: []string{"the task replays on the enrolled device"}, VerificationCommands: []task.VerificationCommand{{RuleID: "go-test", Arguments: []string{"./..."}, WorkingDirectory: "."}}, Risk: task.RiskMedium, IdempotencyKey: "source-task"})
	if err != nil {
		t.Fatal(err)
	}
	sourceMemoryDatabase, err := sourceSession.MemoryDatabase()
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := sourceMemoryDatabase.CreateMemoryCandidate(ctx, memorystore.CreateCandidateInput{
		VaultID: sourceSession.Record.ID, Kind: memory.KindProcedure,
		Scope:     memory.Scope{Kind: memory.ScopeWorkspace, WorkspaceID: createdTask.Task.WorkspaceID, SourceWorkspaceHash: createdTask.Task.SourceWorkspaceHash},
		Statement: "Apply the approved cross-device workspace rule.", Rationale: "The source task provides portable workspace provenance.",
		EvidenceEventIDs: []string{createdTask.Contract.EventID}, SourceActor: "memory-compiler:test", Confidence: 90, Sensitivity: event.SensitivityPrivate,
		IdempotencyKey: "source-memory",
	})
	if err != nil {
		t.Fatal(err)
	}
	approvedMemory, err := sourceMemoryDatabase.TransitionMemory(ctx, memorystore.TransitionInput{VaultID: sourceSession.Record.ID, MemoryID: candidate.ID, ExpectedRevision: 1, NextState: memory.StateApproved, Reason: "approved for cross-device replay", IdempotencyKey: "source-memory-approve"})
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
	if _, err := accepted.Session.database.GetTask(ctx, createdTask.Task.ID); !errors.Is(err, workspacestore.ErrMappingRequired) {
		t.Fatalf("unmapped target task error=%v", err)
	}
	mappingStore, ok := accepted.Session.database.(workspacestore.Store)
	if !ok {
		t.Fatal("target database does not implement workspace mapping")
	}
	inspector, err := gitcli.New()
	if err != nil {
		t.Fatal(err)
	}
	mapper, err := workspaceremap.New(mappingStore, inspector, inspector)
	if err != nil {
		t.Fatal(err)
	}
	mapped, replayed, err := mapper.Map(ctx, workspaceremap.MapInput{VaultID: accepted.Session.Record.ID, TaskID: createdTask.Task.ID, LocalPath: targetWorkspace, ExpectedRevision: 0, IdempotencyKey: "target-workspace-map"})
	if err != nil || replayed || mapped.LocalRoot != targetWorkspace || mapped.VerifiedBaseline != baseline {
		t.Fatalf("mapped=%+v replayed=%v error=%v", mapped, replayed, err)
	}
	targetTask, err := accepted.Session.database.GetTask(ctx, createdTask.Task.ID)
	if err != nil || targetTask.CurrentRevision != 1 || targetTask.WorkspaceRoot != targetWorkspace {
		t.Fatalf("target task=%+v error=%v", targetTask, err)
	}
	targetMemoryDatabase, err := accepted.Session.MemoryDatabase()
	if err != nil {
		t.Fatal(err)
	}
	assembler, err := contextassembly.New(targetMemoryDatabase)
	if err != nil {
		t.Fatal(err)
	}
	assembled, err := assembler.Assemble(ctx, memorycontext.Request{VaultID: accepted.Session.Record.ID, WorkspaceID: targetTask.WorkspaceID, Goal: "reuse the approved workspace rule", MaxCandidates: 16, MaxItems: 4, MaxBytes: 4096})
	if err != nil || len(assembled.Items) != 1 || assembled.Items[0].MemoryID != approvedMemory.ID || assembled.Items[0].Content != approvedMemory.Statement {
		t.Fatalf("assembled=%+v error=%v", assembled, err)
	}
	if _, err := accepted.Session.ReviseTaskContract(ctx, ReviseTaskContractInput{TaskID: targetTask.ID, ExpectedRevision: 1, WorkspaceRoot: targetWorkspace, BaselineCommit: baseline, Goal: "return the synced revision", AllowedPaths: []string{"internal/**"}, ForbiddenActions: []string{"git.push"}, AcceptanceCriteria: []string{"the revision replays on the source device"}, VerificationCommands: []task.VerificationCommand{{RuleID: "go-test", Arguments: []string{"./..."}, WorkingDirectory: "."}}, Risk: task.RiskMedium, IdempotencyKey: "target-revision"}); err != nil {
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

	revoked, err := sourceSession.database.RevokeSyncDevice(ctx, syncstore.RevokeDeviceInput{VaultID: sourceSession.Record.ID, AuthorityDeviceID: accepted.SourceDeviceID, DeviceID: accepted.TargetDeviceID, ExpectedRevision: 1, IdempotencyKey: "revoke-target-device"})
	if err != nil || revoked.State != syncstate.DeviceRevoked {
		t.Fatalf("revoked=%+v error=%v", revoked, err)
	}
	revocationPack, err := sourceSession.ExportNextSyncPack(ctx, 64)
	if err != nil {
		t.Fatal(err)
	}
	revocationReplay, err := accepted.Session.ImportAndApplySyncPack(ctx, ImportSyncPackInput{Encoded: revocationPack.Encoded, DeviceID: accepted.SourceDeviceID, ReceivedAt: time.Now().UTC()})
	if err != nil || revocationReplay.Replay.Batch.AppliedCount != 1 {
		t.Fatalf("revocation replay=%+v error=%v", revocationReplay, err)
	}
	targetIdentity, err := accepted.Session.database.GetSyncDevice(ctx, accepted.Session.Record.ID, accepted.TargetDeviceID)
	if err != nil || targetIdentity.State != syncstate.DeviceRevoked {
		t.Fatalf("target identity=%+v error=%v", targetIdentity, err)
	}
	if _, err := accepted.Session.ExportNextSyncPack(ctx, 64); !errors.Is(err, syncidentity.ErrDeviceRevoked) {
		t.Fatalf("revoked device export error=%v", err)
	}
}

func enrollmentWorkspacePair(t *testing.T) (string, string, string) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("system Git is unavailable")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source-repository")
	target := filepath.Join(root, "target-repository")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	runEnrollmentGit(t, git, source, "init")
	runEnrollmentGit(t, git, source, "config", "user.email", "talos-sync@example.invalid")
	runEnrollmentGit(t, git, source, "config", "user.name", "Talos Sync Test")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("workspace mapping\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runEnrollmentGit(t, git, source, "add", "README.md")
	runEnrollmentGit(t, git, source, "commit", "-m", "baseline")
	baseline := strings.TrimSpace(runEnrollmentGit(t, git, source, "rev-parse", "HEAD^{commit}"))
	runEnrollmentGit(t, git, root, "clone", source, target)
	return source, target, baseline
}

func runEnrollmentGit(t *testing.T, executable, directory string, args ...string) string {
	t.Helper()
	command := exec.Command(executable, append([]string{"-C", directory}, args...)...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
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
