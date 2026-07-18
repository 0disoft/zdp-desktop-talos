package wailsapi

import (
	"errors"
	"testing"

	"github.com/0disoft/zdp-desktop-talos/internal/application/memoryprojection"
	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
)

func TestProjectionServiceReturnsBoundedDeterministicPreview(t *testing.T) {
	t.Parallel()
	database := &serviceDatabase{}
	creator, err := vaultbootstrap.NewCreator(&serviceKeyStore{}, &serviceDatabaseFactory{database: database}, &serviceCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	vault := NewVaultService(creator, nil)
	if created := vault.Create(30, "create-projection-vault"); created.Error != nil {
		t.Fatalf("created=%+v", created)
	}
	result := NewProjectionService(vault).Preview("projection-preview")
	if result.Error != nil || result.Schema != memoryprojection.Schema || result.Included != 0 || !result.Complete || len(result.Files) != 3 || result.BundleSHA256 == "" {
		t.Fatalf("result=%+v", result)
	}
	for _, file := range result.Files {
		if file.Path == "" || file.MediaType == "" || file.SHA256 == "" || file.SizeBytes != len(file.Preview) || file.Truncated {
			t.Fatalf("file=%+v", file)
		}
	}
}

func TestProjectionErrorsDoNotExposeScannerDetails(t *testing.T) {
	t.Parallel()
	mapped := MapError(errors.Join(memoryprojection.ErrSecretFindings, errors.New(`C:\private\token.txt`)), "projection")
	if mapped.Code != "PROJECTION_SECRET_FINDINGS" || mapped.Details != nil || mapped.CorrelationID != "projection" {
		t.Fatalf("mapped=%+v", mapped)
	}
	locked := NewProjectionService(&VaultService{}).Preview("locked")
	if locked.Error == nil || locked.Error.Code != "VAULT_NOT_OPEN" {
		t.Fatalf("locked=%+v", locked)
	}
}
