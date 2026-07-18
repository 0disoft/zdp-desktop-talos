package wailsapi

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/dpapicatalog"
	"github.com/0disoft/zdp-desktop-talos/internal/adapters/folderexchange"
	"github.com/0disoft/zdp-desktop-talos/internal/adapters/localvaultdb"
	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/syncstate"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
)

func TestSyncOverviewAlwaysKeepsInitializedLocalDeviceInsideLimit(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 18, 15, 0, 0, 0, time.UTC)
	local := syncstate.Device{DeviceID: "device-local", PublicKey: ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)), State: syncstate.DeviceActive, Revision: 1, NextSequence: 1, CreatedAt: now, UpdatedAt: now}
	devices := make([]syncstate.Device, 100)
	for index := range devices {
		key := ed25519.PublicKey(make([]byte, ed25519.PublicKeySize))
		key[0] = byte(index + 1)
		devices[index] = syncstate.Device{DeviceID: fmt.Sprintf("device-remote-%03d", index), PublicKey: key, State: syncstate.DeviceActive, Revision: 1, NextSequence: 1, CreatedAt: now, UpdatedAt: now}
	}

	overview := syncOverview(vaultbootstrap.SyncSnapshot{Initialized: true, LocalDevice: local, Devices: devices})
	if len(overview.Devices) != 100 || !overview.Devices[0].Local || overview.Devices[0].DeviceID != local.DeviceID {
		t.Fatalf("bounded overview did not retain local device: %+v", overview.Devices)
	}
	for _, device := range overview.Devices[1:] {
		if device.Local {
			t.Fatalf("remote device marked local: %+v", device)
		}
	}
}

func TestSyncServiceEnrollmentLifecycleAndBoundedOverview(t *testing.T) {
	t.Parallel()
	sourceVault, sourceSync, sourceKeys := newSyncServiceTest(t)
	targetVault, targetSync, _ := newSyncServiceTest(t)
	defer sourceVault.Lock("source-cleanup")
	defer targetVault.Lock("target-cleanup")
	created := sourceVault.Create(30, "source-create")
	if created.Error != nil || created.Vault == nil {
		t.Fatalf("source create=%+v", created)
	}
	overview := sourceSync.Overview("source-overview")
	if overview.Error != nil || overview.Overview == nil || overview.Overview.Initialized || overview.Overview.LocalDeviceID != "" || len(overview.Overview.Devices) != 0 || sourceKeys.has(keyvault.Reference{VaultID: created.Vault.VaultID, KeyID: "sync-device-ed25519-v1"}) {
		t.Fatalf("source overview=%+v", overview)
	}
	if result := sourceSync.CreateEnrollmentOffer(60, "offer-before-init"); result.Error == nil || result.Error.Code != "SYNC_NOT_INITIALIZED" {
		t.Fatalf("offer before init=%+v", result)
	}
	initialized := sourceSync.Initialize("source-initialize")
	if initialized.Error != nil || !initialized.OK {
		t.Fatalf("initialize=%+v", initialized)
	}
	overview = sourceSync.Overview("source-initialized-overview")
	if overview.Error != nil || overview.Overview == nil || !overview.Overview.Initialized || len(overview.Overview.Devices) != 1 || !overview.Overview.Devices[0].Local || len(overview.Overview.Devices[0].KeyFingerprint) != 64 || overview.Overview.Devices[0].NextSequence != "1" {
		t.Fatalf("initialized overview=%+v", overview)
	}
	offer := sourceSync.CreateEnrollmentOffer(60, "offer")
	if offer.Error != nil || offer.Encoded == "" || offer.Secret == "" {
		t.Fatalf("offer=%+v", offer)
	}
	accepted := targetSync.AcceptEnrollmentOffer(offer.Encoded, offer.Secret, "accept")
	if accepted.Error != nil || accepted.Vault == nil || accepted.EncodedAcceptance == "" || accepted.SourceDeviceID == accepted.TargetDeviceID {
		t.Fatalf("accepted=%+v", accepted)
	}
	completed := sourceSync.CompleteEnrollment(accepted.EncodedAcceptance, offer.Secret, "complete")
	if completed.Error != nil || !completed.OK {
		t.Fatalf("completed=%+v", completed)
	}
	overview = sourceSync.Overview("completed-overview")
	if overview.Error != nil || overview.Overview == nil || len(overview.Overview.Devices) != 2 || len(overview.Overview.Enrollments) != 1 || overview.Overview.Enrollments[0].State != "completed" {
		t.Fatalf("completed overview=%+v", overview)
	}

	cancelOffer := sourceSync.CreateEnrollmentOffer(15, "cancel-offer")
	if cancelOffer.Error != nil {
		t.Fatalf("cancel offer=%+v", cancelOffer)
	}
	canceled := sourceSync.CancelEnrollment(cancelOffer.EnrollmentID, "cancel")
	if canceled.Error != nil || !canceled.OK {
		t.Fatalf("canceled=%+v", canceled)
	}
	overview = sourceSync.Overview("canceled-overview")
	if overview.Error != nil || overview.Overview == nil || len(overview.Overview.Enrollments) != 2 || overview.Overview.Enrollments[0].State != "canceled" {
		t.Fatalf("canceled overview=%+v", overview)
	}

	if result := targetSync.AcceptEnrollmentOffer(offer.Encoded, offer.Secret, "accept-while-open"); result.Error == nil || result.Error.Code != "VAULT_ALREADY_OPEN" {
		t.Fatalf("accept while open=%+v", result)
	}
	if result := sourceSync.RevokeDevice(overview.Overview.LocalDeviceID, 1, "self-revoke", "self-revoke"); result.Error == nil || result.Error.Code != "VAULT_INPUT_INVALID" {
		t.Fatalf("self revoke=%+v", result)
	}
}

func TestSyncServiceMapsSafeErrorsWithoutLeakingPaths(t *testing.T) {
	t.Parallel()
	_, service, _ := newSyncServiceTest(t)
	locked := service.Overview("locked")
	if locked.Error == nil || locked.Error.Code != "VAULT_NOT_OPEN" {
		t.Fatalf("locked=%+v", locked)
	}
	invalid := service.ExportFolder("", 64, "folder")
	if invalid.Error == nil || invalid.Error.Code != "SYNC_FOLDER_REQUEST_INVALID" {
		t.Fatalf("invalid=%+v", invalid)
	}
	unavailable := service.ExportGit(`C:\exchange`, 64, "git")
	if unavailable.Error == nil || unavailable.Error.Code != "SYNC_GIT_UNAVAILABLE" {
		t.Fatalf("unavailable=%+v", unavailable)
	}
}

func newSyncServiceTest(t *testing.T) (*VaultService, *SyncService, *syncServiceKeyStore) {
	t.Helper()
	keys := &syncServiceKeyStore{values: map[keyvault.Reference][]byte{}}
	catalog, err := dpapicatalog.New(keys)
	if err != nil {
		t.Fatal(err)
	}
	databases, err := localvaultdb.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	creator, err := vaultbootstrap.NewCreator(keys, databases, catalog)
	if err != nil {
		t.Fatal(err)
	}
	vault := NewVaultService(creator, nil)
	return vault, NewSyncService(vault, folderexchange.New(), nil, nil, nil), keys
}

type syncServiceKeyStore struct {
	mu     sync.Mutex
	values map[keyvault.Reference][]byte
}

func (s *syncServiceKeyStore) Put(_ context.Context, ref keyvault.Reference, secret []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.values[ref]; exists {
		return keyvault.ErrAlreadyExists
	}
	s.values[ref] = append([]byte(nil), secret...)
	return nil
}

func (s *syncServiceKeyStore) Get(_ context.Context, ref keyvault.Reference) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	secret, exists := s.values[ref]
	if !exists {
		return nil, keyvault.ErrNotFound
	}
	return append([]byte(nil), secret...), nil
}

func (s *syncServiceKeyStore) Rotate(_ context.Context, ref keyvault.Reference, secret []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.values[ref]; !exists {
		return keyvault.ErrNotFound
	}
	s.values[ref] = append([]byte(nil), secret...)
	return nil
}

func (s *syncServiceKeyStore) Delete(_ context.Context, ref keyvault.Reference) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.values[ref]; !exists {
		return keyvault.ErrNotFound
	}
	delete(s.values, ref)
	return nil
}

func (s *syncServiceKeyStore) has(ref keyvault.Reference) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, exists := s.values[ref]
	return exists
}
