package bootstrap

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/dpapikeyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
)

const instanceKeySize = 32

var instanceKeyReference = keyvault.Reference{VaultID: "talos-system", KeyID: "single-instance-ipc-v1"}

func LoadOrCreateInstanceKey(ctx context.Context, localDataRoot string) ([instanceKeySize]byte, error) {
	var result [instanceKeySize]byte
	keys, err := dpapikeyvault.Open(filepath.Join(localDataRoot, "keys"))
	if err != nil {
		return result, fmt.Errorf("open protected instance-key storage: %w", err)
	}
	stored, err := keys.Get(ctx, instanceKeyReference)
	if err == nil {
		return instanceKey(stored)
	}
	if !errors.Is(err, keyvault.ErrNotFound) {
		return result, fmt.Errorf("load protected instance key: %w", err)
	}

	candidate := make([]byte, instanceKeySize)
	if _, err := io.ReadFull(rand.Reader, candidate); err != nil {
		return result, fmt.Errorf("generate instance key: %w", err)
	}
	defer clear(candidate)
	if err := keys.Put(ctx, instanceKeyReference, candidate); err != nil {
		if !errors.Is(err, keyvault.ErrAlreadyExists) {
			return result, fmt.Errorf("persist protected instance key: %w", err)
		}
		stored, err = keys.Get(ctx, instanceKeyReference)
		if err != nil {
			return result, fmt.Errorf("load concurrently created instance key: %w", err)
		}
		return instanceKey(stored)
	}
	copy(result[:], candidate)
	return result, nil
}

func instanceKey(value []byte) ([instanceKeySize]byte, error) {
	var result [instanceKeySize]byte
	defer clear(value)
	if len(value) != instanceKeySize {
		return result, keyvault.ErrCorrupt
	}
	copy(result[:], value)
	return result, nil
}
