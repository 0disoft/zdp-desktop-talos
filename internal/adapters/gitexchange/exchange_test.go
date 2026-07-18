package gitexchange

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

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/folderexchange"
	"github.com/0disoft/zdp-desktop-talos/internal/adapters/gitcli"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspace"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/syncexchange"
)

func TestExchangeWritesOnlyIntoCleanBranchAndReadsCommittedPacks(t *testing.T) {
	t.Parallel()
	root := newRepository(t)
	inspector, err := gitcli.New()
	if err != nil {
		t.Fatal(err)
	}
	exchange, err := New(inspector, inspector, folderexchange.New())
	if err != nil {
		t.Fatal(err)
	}
	descriptor := testDescriptor(1, 2, "pack-1")
	encoded := []byte("encrypted-pack-one")
	stored, replay, err := exchange.WritePack(context.Background(), root, descriptor, encoded)
	if err != nil || replay || !strings.HasPrefix(stored.RelativePath, DataDirectory+"/vaults/") {
		t.Fatalf("stored=%+v replay=%v error=%v", stored, replay, err)
	}
	snapshot, err := inspector.Inspect(context.Background(), root)
	if err != nil || !snapshot.Dirty || !onlyPreparedPackChange(snapshot.Changes, stored.RelativePath) {
		t.Fatalf("snapshot=%+v error=%v", snapshot, err)
	}
	if _, _, err := exchange.WritePack(context.Background(), root, descriptor, encoded); !errors.Is(err, ErrRepositoryDirty) {
		t.Fatalf("dirty replay error=%v", err)
	}
	runGit(t, root, "add", "--", DataDirectory)
	runGit(t, root, "commit", "-m", "add encrypted pack")
	stored, replay, err = exchange.WritePack(context.Background(), root, descriptor, encoded)
	if err != nil || !replay || stored.SequenceStart != 1 || stored.SequenceEnd != 2 {
		t.Fatalf("committed replay stored=%+v replay=%v error=%v", stored, replay, err)
	}
	packs, err := exchange.ReadPacks(context.Background(), root, descriptor.VaultID, descriptor.DeviceID)
	if err != nil || len(packs) != 1 || !bytes.Equal(packs[0].Encoded, encoded) || !strings.HasPrefix(packs[0].RelativePath, DataDirectory+"/") {
		t.Fatalf("packs=%+v error=%v", packs, err)
	}
	clearPacks(packs)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := exchange.ReadPacks(context.Background(), root, descriptor.VaultID, descriptor.DeviceID); !errors.Is(err, ErrRepositoryDirty) {
		t.Fatalf("dirty import error=%v", err)
	}
}

func TestExchangeRejectsIgnoredPacksDetachedHeadAndNestedRoot(t *testing.T) {
	t.Parallel()
	root := newRepository(t)
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(DataDirectory+"/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", ".gitignore")
	runGit(t, root, "commit", "-m", "ignore exchange data")
	inspector, err := gitcli.New()
	if err != nil {
		t.Fatal(err)
	}
	exchange, err := New(inspector, inspector, folderexchange.New())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := exchange.WritePack(context.Background(), root, testDescriptor(3, 3, "ignored-pack"), []byte("encrypted-pack-two")); !errors.Is(err, ErrPackUntracked) {
		t.Fatalf("ignored pack error=%v", err)
	}
	runGit(t, root, "checkout", "--detach")
	if _, err := exchange.ReadPacks(context.Background(), root, "vault-test", "device-test"); !errors.Is(err, ErrDetachedHead) {
		t.Fatalf("detached import error=%v", err)
	}
	runGit(t, root, "checkout", "main")
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := exchange.ReadPacks(context.Background(), nested, "vault-test", "device-test"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nested root error=%v", err)
	}
}

func TestReadClearsPackBytesWhenRepositoryChanges(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	now := time.Date(2026, 7, 18, 16, 0, 0, 0, time.UTC)
	first := cleanTestSnapshot(root, strings.Repeat("a", 40), now)
	second := cleanTestSnapshot(root, strings.Repeat("b", 40), now.Add(time.Second))
	inspector := &snapshotSequence{snapshots: []workspace.RepositorySnapshot{first, second}}
	files := &memoryExchange{packs: []syncexchange.ReadPack{{StoredPack: syncexchange.StoredPack{RelativePath: "vaults/v/packs/d/2026/07/00000000000000000001-00000000000000000001-x.talos-pack", SequenceStart: 1, SequenceEnd: 1}, Encoded: []byte("sensitive-pack")}}}
	exchange, err := New(inspector, trackedAlways{}, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exchange.ReadPacks(context.Background(), root, "vault-test", "device-test"); !errors.Is(err, ErrRepositoryChanged) {
		t.Fatalf("changed repository error=%v", err)
	}
	if !bytes.Equal(files.packs[0].Encoded, make([]byte, len(files.packs[0].Encoded))) {
		t.Fatal("failed Git import retained pack bytes")
	}
}

func testDescriptor(start, end uint64, packID string) syncexchange.PackDescriptor {
	return syncexchange.PackDescriptor{VaultID: "vault-test", DeviceID: "device-test", PackID: packID, SequenceStart: start, SequenceEnd: end, CreatedAt: time.Date(2026, 7, 18, 15, 30, 0, 0, time.UTC)}
}

func newRepository(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("system Git is unavailable")
	}
	root := filepath.Join(t.TempDir(), "exchange-repository")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "init", "-b", "main")
	runGit(t, root, "config", "user.email", "talos-git-exchange@example.invalid")
	runGit(t, root, "config", "user.name", "Talos Git Exchange Test")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("encrypted Talos pack exchange\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "initialize exchange repository")
	return root
}

func runGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-c", "core.longpaths=true", "-C", root}, args...)...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}

func cleanTestSnapshot(root, commit string, capturedAt time.Time) workspace.RepositorySnapshot {
	return workspace.RepositorySnapshot{Root: root, BaselineCommit: commit, HeadRef: "main", CapturedAt: capturedAt}
}

type snapshotSequence struct {
	snapshots []workspace.RepositorySnapshot
	index     int
}

func (s *snapshotSequence) Inspect(context.Context, string) (workspace.RepositorySnapshot, error) {
	if s.index >= len(s.snapshots) {
		return workspace.RepositorySnapshot{}, errors.New("test snapshot sequence exhausted")
	}
	result := s.snapshots[s.index]
	s.index++
	return result, nil
}

type trackedAlways struct{}

func (trackedAlways) IsTracked(context.Context, string, string) (bool, error) { return true, nil }

type memoryExchange struct {
	packs []syncexchange.ReadPack
}

func (m *memoryExchange) WritePack(context.Context, string, syncexchange.PackDescriptor, []byte) (syncexchange.StoredPack, bool, error) {
	return syncexchange.StoredPack{}, false, errors.New("unexpected test write")
}

func (m *memoryExchange) ReadPacks(context.Context, string, string, string) ([]syncexchange.ReadPack, error) {
	return m.packs, nil
}
