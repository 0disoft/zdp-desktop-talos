package bootstrap

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/dpapikeyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
)

const instanceKeySize = 32

const (
	instanceKeyReadAttempts = 8
	instanceKeyReadDelay    = 5 * time.Millisecond
)

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
		stored, err = retryInstanceKeyRead(ctx, keys, err)
		if err == nil {
			return instanceKey(stored)
		}
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
		stored, err = retryInstanceKeyRead(ctx, keys, nil)
		if err != nil {
			return result, fmt.Errorf("load concurrently created instance key: %w", err)
		}
		return instanceKey(stored)
	}
	copy(result[:], candidate)
	return result, nil
}

func retryInstanceKeyRead(ctx context.Context, keys keyvault.Store, initial error) ([]byte, error) {
	lastErr := initial
	delay := instanceKeyReadDelay
	for attempt := 0; attempt < instanceKeyReadAttempts; attempt++ {
		if attempt > 0 || initial != nil {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
			if delay < 40*time.Millisecond {
				delay *= 2
			}
		}
		stored, err := keys.Get(ctx, instanceKeyReference)
		if err == nil {
			return stored, nil
		}
		lastErr = err
	}
	return nil, lastErr
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
