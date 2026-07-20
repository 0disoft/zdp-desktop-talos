package wailsapi

import (
	"testing"

	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
)

func TestHealthServiceReportsLiveVaultAndWorkerAvailability(t *testing.T) {
	t.Parallel()

	creator, err := vaultbootstrap.NewCreator(
		&serviceKeyStore{},
		&serviceDatabaseFactory{database: &serviceDatabase{}},
		&serviceCatalog{},
	)
	if err != nil {
		t.Fatal(err)
	}
	vaultService := NewVaultService(creator, nil)
	service := NewHealthService(vaultService, true)

	locked := service.Snapshot()
	if locked.WorkerStatus != "ready" || locked.VaultStatus != "locked" {
		t.Fatalf("locked snapshot=%+v", locked)
	}
	if result := vaultService.Create(30, "health-create"); result.Error != nil {
		t.Fatalf("create result=%+v", result)
	}
	unlocked := service.Snapshot()
	if unlocked.WorkerStatus != "ready" || unlocked.VaultStatus != "unlocked" {
		t.Fatalf("unlocked snapshot=%+v", unlocked)
	}
}

func TestHealthServiceFailsClosedWithoutRuntimeDependencies(t *testing.T) {
	t.Parallel()

	snapshot := NewHealthService(nil, false).Snapshot()
	if snapshot.WorkerStatus != "unavailable" || snapshot.VaultStatus != "unavailable" || snapshot.LatestError != "" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}
