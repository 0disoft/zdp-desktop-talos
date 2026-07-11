package localvaultdb

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultdb"
)

func TestFactoryCreatesOpaqueExclusiveDatabaseAndRemovesSidecars(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	factory, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	database, err := factory.Create(context.Background(), "vault-alpha", "vault-kek-v1", key)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := factory.path("vault-alpha")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database was not created: %v", err)
	}
	if _, err := factory.Create(context.Background(), "vault-alpha", "vault-kek-v1", key); !errors.Is(err, vaultdb.ErrAlreadyExists) {
		t.Fatalf("duplicate create error = %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := factory.Open(context.Background(), "vault-alpha", "vault-kek-v1", key)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-wal", []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := factory.Remove(context.Background(), "vault-alpha"); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{path, path + "-wal", path + "-shm", path + ".blobs"} {
		if _, err := os.Stat(candidate); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s survived removal: %v", candidate, err)
		}
	}
}

func TestFactoryRefusesDatabaseRemovalWhileArtifactsRemain(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	factory, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	database, err := factory.Create(context.Background(), "vault-retained", "vault-kek-v1", make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	path, _ := factory.path("vault-retained")
	if err := os.WriteFile(path+".blobs/retained.blob", []byte("ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := factory.Remove(context.Background(), "vault-retained"); err == nil {
		t.Fatal("database removal ignored retained artifacts")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database was removed before artifact preflight: %v", err)
	}
	if err := factory.Purge(context.Background(), "vault-retained"); err == nil {
		t.Fatal("purge accepted an unexpected artifact filename")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database was removed after unsafe purge preflight: %v", err)
	}
	if err := os.Remove(path + ".blobs/retained.blob"); err != nil {
		t.Fatal(err)
	}
	validBlob := strings.Repeat("a", 64) + ".blob"
	if err := os.WriteFile(path+".blobs/"+validBlob, []byte("ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := factory.Purge(context.Background(), "vault-retained"); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{path, path + ".blobs"} {
		if _, err := os.Stat(candidate); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s survived purge: %v", candidate, err)
		}
	}
}
