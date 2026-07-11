package localvaultdb

import (
	"context"
	"errors"
	"os"
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
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Stat(candidate); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s survived removal: %v", candidate, err)
		}
	}
}
