//go:build windows

package bootstrap

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
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
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
