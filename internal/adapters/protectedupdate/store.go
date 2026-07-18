package protectedupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/releaseupdate"
	"github.com/0disoft/zdp-desktop-talos/internal/id"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/updatejournal"
)

const documentVersion = 1

type Store struct {
	keys keyvault.Store
	mu   sync.Mutex
}

type document struct {
	Version     int                       `json:"version"`
	Preparation releaseupdate.Preparation `json:"preparation"`
}

func New(keys keyvault.Store) (*Store, error) {
	if keys == nil {
		return nil, updatejournal.ErrInvalidRecord
	}
	return &Store{keys: keys}, nil
}

func (s *Store) Save(ctx context.Context, preparation releaseupdate.Preparation) error {
	if ctx == nil || releaseupdate.ValidatePreparation(preparation) != nil {
		return updatejournal.ErrInvalidRecord
	}
	encoded, err := json.Marshal(document{Version: documentVersion, Preparation: preparation})
	if err != nil {
		return fmt.Errorf("encode protected update preparation: %w", err)
	}
	ref := reference(preparation.VaultID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.keys.Get(ctx, ref); errors.Is(err, keyvault.ErrNotFound) {
		if err := s.keys.Put(ctx, ref, encoded); err != nil {
			return fmt.Errorf("persist protected update preparation: %w", err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect protected update preparation: %w", err)
	}
	if err := s.keys.Rotate(ctx, ref, encoded); err != nil {
		return fmt.Errorf("replace protected update preparation: %w", err)
	}
	return nil
}

func (s *Store) Load(ctx context.Context, vaultID string) (releaseupdate.Preparation, error) {
	if ctx == nil || !id.IsUUIDv7(vaultID) {
		return releaseupdate.Preparation{}, updatejournal.ErrInvalidRecord
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	encoded, err := s.keys.Get(ctx, reference(vaultID))
	if errors.Is(err, keyvault.ErrNotFound) {
		return releaseupdate.Preparation{}, updatejournal.ErrNotFound
	}
	if err != nil {
		return releaseupdate.Preparation{}, fmt.Errorf("read protected update preparation: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var stored document
	if err := decoder.Decode(&stored); err != nil || stored.Version != documentVersion || stored.Preparation.VaultID != vaultID || releaseupdate.ValidatePreparation(stored.Preparation) != nil {
		return releaseupdate.Preparation{}, updatejournal.ErrCorrupt
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return releaseupdate.Preparation{}, updatejournal.ErrCorrupt
	}
	return stored.Preparation, nil
}

func (s *Store) Delete(ctx context.Context, vaultID string) error {
	if ctx == nil || !id.IsUUIDv7(vaultID) {
		return updatejournal.ErrInvalidRecord
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.keys.Delete(ctx, reference(vaultID)); errors.Is(err, keyvault.ErrNotFound) {
		return updatejournal.ErrNotFound
	} else if err != nil {
		return fmt.Errorf("delete protected update preparation: %w", err)
	}
	return nil
}

func reference(vaultID string) keyvault.Reference {
	return keyvault.Reference{VaultID: vaultID, KeyID: updatejournal.ProtectedRecordKeyID}
}
