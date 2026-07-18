//go:build !windows

package localvaultdb

import (
	"errors"
	"fmt"
	"os"

	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultbackup"
)

func promoteBackupExclusive(staging, destination string) error {
	if err := os.Link(staging, destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return vaultbackup.ErrAlreadyExists
		}
		return fmt.Errorf("link encrypted Vault backup into place: %w", err)
	}
	if err := os.Remove(staging); err != nil {
		_ = os.Remove(destination)
		return fmt.Errorf("remove linked Vault backup staging file: %w", err)
	}
	return nil
}
