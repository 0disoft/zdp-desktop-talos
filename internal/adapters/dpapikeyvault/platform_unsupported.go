//go:build !windows

package dpapikeyvault

import "github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"

func platformAvailable() bool {
	return false
}

func platformProtect(_ []byte, _ []byte) ([]byte, error) {
	return nil, keyvault.ErrUnsupported
}

func platformUnprotect(_ []byte, _ []byte) ([]byte, error) {
	return nil, keyvault.ErrUnsupported
}

func platformCommit(_, _ string, _ bool) error {
	return keyvault.ErrUnsupported
}
