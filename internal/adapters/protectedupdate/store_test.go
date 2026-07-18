package protectedupdate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/releaseupdate"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/updatejournal"
)

const (
	testVaultID       = "019f5f1a-1000-7000-8000-000000000001"
	testReleaseID     = "019f5f1a-1000-7000-8000-000000000002"
	testPreparationID = "019f5f1a-1000-7000-8000-000000000003"
)

func TestStoreRoundTripsAndReplacesProtectedPreparation(t *testing.T) {
	t.Parallel()
	keys := &memoryKeys{values: map[keyvault.Reference][]byte{}}
	store, err := New(keys)
	if err != nil {
		t.Fatal(err)
	}
	preparation := validPreparation()
	if err := store.Save(context.Background(), preparation); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background(), preparation.VaultID)
	if err != nil || loaded.PreparationID != preparation.PreparationID {
		t.Fatalf("Load() = %+v, %v", loaded, err)
	}
	preparation.TargetVersion = "0.34.0"
	if err := store.Save(context.Background(), preparation); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.Load(context.Background(), preparation.VaultID)
	if err != nil || loaded.TargetVersion != "0.34.0" || keys.rotateCount != 1 {
		t.Fatalf("replaced preparation = %+v, %v; rotates=%d", loaded, err, keys.rotateCount)
	}
	if err := store.Delete(context.Background(), preparation.VaultID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(context.Background(), preparation.VaultID); !errors.Is(err, updatejournal.ErrNotFound) {
		t.Fatalf("Load() after Delete error = %v", err)
	}
}

func TestStoreRejectsCorruptOrCrossVaultPreparation(t *testing.T) {
	t.Parallel()
	keys := &memoryKeys{values: map[keyvault.Reference][]byte{}}
	store, _ := New(keys)
	keys.values[reference(testVaultID)] = []byte(`{"version":1,"preparation":{"vault_id":"other"}}`)
	if _, err := store.Load(context.Background(), testVaultID); !errors.Is(err, updatejournal.ErrCorrupt) {
		t.Fatalf("Load() corrupt error = %v", err)
	}
}

func validPreparation() releaseupdate.Preparation {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	return releaseupdate.Preparation{
		Schema: releaseupdate.PreparationSchema, PreparationID: testPreparationID, ReleaseID: testReleaseID,
		Channel: releaseupdate.ChannelBeta, TargetVersion: "0.33.0", CurrentVersion: "0.32.0", TargetOS: "windows", TargetArchitecture: "amd64",
		ManifestSHA256: repeat("a", 64), ArtifactName: "talos-setup.exe", ArtifactSHA256: repeat("b", 64), SigningKeyID: repeat("c", 64),
		VaultID: testVaultID, VaultRevision: 2, BackupID: "019f5f1a-1000-7000-8000-000000000004", BackupCiphertextSHA256: repeat("d", 64),
		BackupSourceSchema: 23, BackupTargetSchema: 23, PreparedAt: now, ExpiresAt: now.Add(time.Hour),
	}
}

func repeat(value string, count int) string {
	result := ""
	for len(result) < count {
		result += value
	}
	return result[:count]
}

type memoryKeys struct {
	values      map[keyvault.Reference][]byte
	rotateCount int
}

func (s *memoryKeys) Put(_ context.Context, ref keyvault.Reference, value []byte) error {
	if _, exists := s.values[ref]; exists {
		return keyvault.ErrAlreadyExists
	}
	s.values[ref] = append([]byte(nil), value...)
	return nil
}

func (s *memoryKeys) Get(_ context.Context, ref keyvault.Reference) ([]byte, error) {
	value, exists := s.values[ref]
	if !exists {
		return nil, keyvault.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s *memoryKeys) Rotate(_ context.Context, ref keyvault.Reference, value []byte) error {
	if _, exists := s.values[ref]; !exists {
		return keyvault.ErrNotFound
	}
	s.rotateCount++
	s.values[ref] = append([]byte(nil), value...)
	return nil
}

func (s *memoryKeys) Delete(_ context.Context, ref keyvault.Reference) error {
	if _, exists := s.values[ref]; !exists {
		return keyvault.ErrNotFound
	}
	delete(s.values, ref)
	return nil
}
