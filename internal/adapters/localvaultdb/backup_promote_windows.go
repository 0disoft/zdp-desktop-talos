//go:build windows

package localvaultdb

import (
	"errors"
	"fmt"
	"os"

	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultbackup"
)

func promoteBackupExclusive(staging, destination string) error {
	if _, err := os.Lstat(destination); err == nil {
		return vaultbackup.ErrAlreadyExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect Vault backup promotion destination: %w", err)
	}
	if err := os.Rename(staging, destination); err != nil {
		if _, inspectErr := os.Lstat(destination); inspectErr == nil {
			return vaultbackup.ErrAlreadyExists
		}
		return fmt.Errorf("promote encrypted Vault backup: %w", err)
	}
	return nil
}
