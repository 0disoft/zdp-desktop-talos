//go:build windows

package bootstrap

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
)

func TestVaultCreatorRebuildDiscoversAndReopensProtectedVault(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	ctx := context.Background()

	first, err := NewVaultCreator(root)
	if err != nil {
		t.Fatal(err)
	}
	created, err := first.Create(ctx, vaultbootstrap.CreateInput{RetentionDays: 90})
	if err != nil {
		t.Fatal(err)
	}
	vaultID := created.Record.ID
	artifactMarker := []byte("windows-bootstrap-artifact-private-marker")
	storedArtifact, err := created.StoreArtifact(ctx, vaultbootstrap.StoreArtifactInput{
		SchemaVersion: 1, Sensitivity: event.SensitivitySensitive,
		ContentType: "text/plain; charset=utf-8", Payload: artifactMarker,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := created.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := NewVaultCreator(root)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := restarted.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].VaultID != vaultID {
		t.Fatalf("entries after restart = %+v", entries)
	}
	reopened, err := restarted.Open(ctx, vaultID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Record.ID != vaultID || reopened.Record.RetentionDays != 90 {
		t.Fatalf("reopened = %+v", reopened.Record)
	}
	loadedArtifact, payload, err := reopened.LoadArtifact(ctx, storedArtifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loadedArtifact != storedArtifact || !bytes.Equal(payload, artifactMarker) {
		t.Fatalf("loaded artifact=%+v payload=%q", loadedArtifact, payload)
	}
	clear(payload)
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}

	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		if filepath.Ext(path) == ".dpapi" {
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytes.Contains(contents, []byte(vaultID)) || bytes.Contains([]byte(filepath.Base(path)), []byte(vaultID)) {
				t.Fatalf("Vault ID leaked in protected catalog or key record %s", filepath.Base(path))
			}
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(contents, artifactMarker) {
			t.Fatalf("artifact plaintext leaked in %s", filepath.Base(path))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInstanceKeyPersistsThroughDPAPIWithoutPlaintext(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	ctx := context.Background()
	first, err := LoadOrCreateInstanceKey(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateInstanceKey(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first == [instanceKeySize]byte{} {
		t.Fatal("instance key was not stable and nonzero")
	}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(contents, first[:]) {
			t.Fatalf("instance key leaked in plaintext file %s", filepath.Base(path))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInstanceKeyConcurrentFirstLaunchConverges(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const callers = 8
	keys := make(chan [instanceKeySize]byte, callers)
	errorsFound := make(chan error, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			key, err := LoadOrCreateInstanceKey(context.Background(), root)
			if err != nil {
				errorsFound <- err
				return
			}
			keys <- key
		}()
	}
	group.Wait()
	close(keys)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
	var expected [instanceKeySize]byte
	for key := range keys {
		if expected == [instanceKeySize]byte{} {
			expected = key
			continue
		}
		if key != expected {
			t.Fatal("concurrent first launch produced different instance keys")
		}
	}
}
