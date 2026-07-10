//go:build windows

package dpapikeyvault

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
)

func TestStoreLifecycleAndPersistence(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ref := keyvault.Reference{VaultID: "vault-0190", KeyID: "vault-kek"}
	first := bytes.Repeat([]byte{0x41}, 32)
	second := bytes.Repeat([]byte{0x42}, 32)

	if err := store.Put(context.Background(), ref, first); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), ref, first); !errors.Is(err, keyvault.ErrAlreadyExists) {
		t.Fatalf("duplicate put error = %v", err)
	}

	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, first) {
		t.Fatal("reopened store returned a different secret")
	}

	if err := reopened.Rotate(context.Background(), ref, second); err != nil {
		t.Fatal(err)
	}
	got, err = reopened.Get(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, second) {
		t.Fatal("rotated store returned a different secret")
	}

	if err := reopened.Delete(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Get(context.Background(), ref); !errors.Is(err, keyvault.ErrNotFound) {
		t.Fatalf("get after delete error = %v", err)
	}
	if err := reopened.Delete(context.Background(), ref); !errors.Is(err, keyvault.ErrNotFound) {
		t.Fatalf("second delete error = %v", err)
	}
}

func TestStoreDoesNotPersistPlaintextAndBindsReference(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	source := keyvault.Reference{VaultID: "vault-source", KeyID: "vault-kek"}
	target := keyvault.Reference{VaultID: "vault-target", KeyID: "vault-kek"}
	secret := []byte("talos-dpapi-private-marker-32b!")
	if err := store.Put(context.Background(), source, secret); err != nil {
		t.Fatal(err)
	}

	sourceRecord, err := os.ReadFile(store.pathFor("vault-source\x00vault-kek"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sourceRecord, secret) {
		t.Fatal("protected record contains the plaintext secret")
	}
	if err := os.WriteFile(store.pathFor("vault-target\x00vault-kek"), sourceRecord, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), target); !errors.Is(err, keyvault.ErrUnseal) {
		t.Fatalf("moved record error = %v", err)
	}
}

func TestStoreRejectsInvalidInputAndCorruption(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	invalid := []keyvault.Reference{
		{},
		{VaultID: "../vault", KeyID: "key"},
		{VaultID: "vault", KeyID: "..\\key"},
	}
	for _, ref := range invalid {
		if err := store.Put(context.Background(), ref, []byte("secret")); !errors.Is(err, keyvault.ErrInvalidReference) {
			t.Fatalf("Put(%+v) error = %v", ref, err)
		}
	}

	ref := keyvault.Reference{VaultID: "vault", KeyID: "key"}
	if err := store.Put(context.Background(), ref, nil); !errors.Is(err, keyvault.ErrInvalidSecret) {
		t.Fatalf("empty secret error = %v", err)
	}
	if err := store.Rotate(context.Background(), ref, []byte("secret")); !errors.Is(err, keyvault.ErrNotFound) {
		t.Fatalf("rotate missing error = %v", err)
	}

	canonical, err := canonicalReference(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(store.pathFor(canonical)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.pathFor(canonical), []byte("not-a-record"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), ref); !errors.Is(err, keyvault.ErrCorrupt) {
		t.Fatalf("corrupt record error = %v", err)
	}
}

func TestStoreHonorsCanceledContext(t *testing.T) {
	t.Parallel()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ref := keyvault.Reference{VaultID: "vault", KeyID: "key"}
	if err := store.Put(ctx, ref, []byte("secret")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled put error = %v", err)
	}
}
