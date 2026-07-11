package bootstrap

import (
	"fmt"
	"path/filepath"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/dpapikeyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/adapters/localvaultdb"
	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
)

func NewVaultCreator(localDataRoot string) (*vaultbootstrap.Creator, error) {
	if localDataRoot == "" {
		return nil, fmt.Errorf("local data root is empty")
	}
	keys, err := dpapikeyvault.Open(filepath.Join(localDataRoot, "keys"))
	if err != nil {
		return nil, fmt.Errorf("initialize protected key storage: %w", err)
	}
	databases, err := localvaultdb.New(filepath.Join(localDataRoot, "vaults"))
	if err != nil {
		return nil, fmt.Errorf("initialize Vault database storage: %w", err)
	}
	creator, err := vaultbootstrap.NewCreator(keys, databases)
	if err != nil {
		return nil, fmt.Errorf("initialize Vault creator: %w", err)
	}
	return creator, nil
}
