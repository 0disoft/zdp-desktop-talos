package localvaultdb

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/sqliteevent"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultdb"
	"github.com/0disoft/zdp-desktop-talos/internal/security/envelope"
)

type Factory struct {
	root string
}

type database struct {
	*sqliteevent.Store
	sealer *envelope.Sealer
}

func (d *database) Close() error {
	err := d.Store.Close()
	d.sealer.Destroy()
	return err
}

func New(root string) (*Factory, error) {
	if root == "" {
		return nil, errors.New("Vault database root is empty")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve Vault database root: %w", err)
	}
	return &Factory{root: filepath.Clean(absolute)}, nil
}

func (f *Factory) Create(ctx context.Context, vaultID, keyID string, key []byte) (vaultdb.Database, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := f.path(vaultID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(f.root, 0o700); err != nil {
		return nil, fmt.Errorf("create Vault database root: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, vaultdb.ErrAlreadyExists
		}
		return nil, fmt.Errorf("reserve Vault database: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("close reserved Vault database: %w", err)
	}

	sealer, err := envelope.NewSealer(keyID, key)
	if err != nil {
		_, _ = removeDatabaseFiles(path)
		return nil, err
	}
	store, err := sqliteevent.Open(path, sealer)
	if err != nil {
		_, _ = removeDatabaseFiles(path)
		return nil, err
	}
	return &database{Store: store, sealer: sealer}, nil
}

func (f *Factory) Remove(ctx context.Context, vaultID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := f.path(vaultID)
	if err != nil {
		return err
	}
	removed, err := removeDatabaseFiles(path)
	if err != nil {
		return err
	}
	if !removed {
		return vaultdb.ErrNotFound
	}
	return nil
}

func (f *Factory) path(vaultID string) (string, error) {
	if vaultID == "" || len(vaultID) > 128 {
		return "", errors.New("Vault ID is invalid")
	}
	hash := sha256.Sum256([]byte("talos.vault-db/v1\x00" + vaultID))
	return filepath.Join(f.root, fmt.Sprintf("%x.db", hash[:])), nil
}

func removeDatabaseFiles(path string) (bool, error) {
	removed := false
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(candidate); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return removed, fmt.Errorf("remove Vault database file: %w", err)
		}
		removed = true
	}
	return removed, nil
}
