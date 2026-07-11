package dpapicatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/id"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultcatalog"
)

const (
	catalogVersion = 2
	maxEntries     = 256
)

var catalogReference = keyvault.Reference{VaultID: "talos-system", KeyID: "vault-catalog-v1"}

type Catalog struct {
	keys keyvault.Store
	mu   sync.Mutex
}

type document struct {
	Version int             `json:"version"`
	Entries []documentEntry `json:"entries"`
}

type documentEntry struct {
	VaultID   string `json:"vault_id"`
	CreatedAt string `json:"created_at"`
	State     string `json:"state,omitempty"`
}

func New(keys keyvault.Store) (*Catalog, error) {
	if keys == nil {
		return nil, vaultcatalog.ErrCorrupt
	}
	return &Catalog{keys: keys}, nil
}

func (c *Catalog) List(ctx context.Context) ([]vaultcatalog.Entry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, _, err := c.load(ctx)
	return filterState(entries, vaultcatalog.StateActive), err
}

func (c *Catalog) PendingPurges(ctx context.Context) ([]vaultcatalog.Entry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, _, err := c.load(ctx)
	return filterState(entries, vaultcatalog.StatePurgePending), err
}

func (c *Catalog) Add(ctx context.Context, entry vaultcatalog.Entry) error {
	entry.State = vaultcatalog.StateActive
	if err := validateEntry(entry); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, exists, err := c.load(ctx)
	if err != nil {
		return err
	}
	for _, current := range entries {
		if current.VaultID == entry.VaultID {
			return vaultcatalog.ErrConflict
		}
	}
	if len(entries) >= maxEntries {
		return fmt.Errorf("%w: entry limit exceeded", vaultcatalog.ErrCorrupt)
	}
	entries = append(entries, entry)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].CreatedAt.Equal(entries[j].CreatedAt) {
			return entries[i].VaultID < entries[j].VaultID
		}
		return entries[i].CreatedAt.Before(entries[j].CreatedAt)
	})
	return c.store(ctx, entries, exists)
}

func (c *Catalog) MarkPurgePending(ctx context.Context, target vaultcatalog.Entry) error {
	target.State = vaultcatalog.StateActive
	if err := validateEntry(target); err != nil {
		return vaultcatalog.ErrNotFound
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, exists, err := c.load(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return vaultcatalog.ErrNotFound
	}
	found := false
	for index := range entries {
		if entries[index].VaultID == target.VaultID && entries[index].CreatedAt.Equal(target.CreatedAt) {
			if entries[index].State == vaultcatalog.StatePurgePending {
				return nil
			}
			entries[index].State = vaultcatalog.StatePurgePending
			found = true
			break
		}
	}
	if !found {
		return vaultcatalog.ErrNotFound
	}
	return c.store(ctx, entries, true)
}

func (c *Catalog) Remove(ctx context.Context, target vaultcatalog.Entry) error {
	target.State = vaultcatalog.StateActive
	if err := validateEntry(target); err != nil {
		return vaultcatalog.ErrNotFound
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, exists, err := c.load(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return vaultcatalog.ErrNotFound
	}
	filtered := make([]vaultcatalog.Entry, 0, len(entries))
	found := false
	for _, entry := range entries {
		if entry.VaultID == target.VaultID && entry.CreatedAt.Equal(target.CreatedAt) {
			found = true
			continue
		}
		filtered = append(filtered, entry)
	}
	if !found {
		return vaultcatalog.ErrNotFound
	}
	if len(filtered) == 0 {
		if err := c.keys.Delete(ctx, catalogReference); err != nil && !errors.Is(err, keyvault.ErrNotFound) {
			return fmt.Errorf("delete empty Vault catalog: %w", err)
		}
		return nil
	}
	return c.store(ctx, filtered, true)
}

func (c *Catalog) load(ctx context.Context) ([]vaultcatalog.Entry, bool, error) {
	encoded, err := c.keys.Get(ctx, catalogReference)
	if errors.Is(err, keyvault.ErrNotFound) {
		return []vaultcatalog.Entry{}, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read Vault catalog: %w", err)
	}
	var stored document
	if err := json.Unmarshal(encoded, &stored); err != nil || (stored.Version != 1 && stored.Version != catalogVersion) || len(stored.Entries) > maxEntries {
		return nil, true, vaultcatalog.ErrCorrupt
	}
	entries := make([]vaultcatalog.Entry, 0, len(stored.Entries))
	seen := make(map[string]struct{}, len(stored.Entries))
	for _, raw := range stored.Entries {
		createdAt, err := time.Parse(time.RFC3339Nano, raw.CreatedAt)
		if err != nil {
			return nil, true, vaultcatalog.ErrCorrupt
		}
		state := vaultcatalog.State(raw.State)
		if stored.Version == 1 && state == "" {
			state = vaultcatalog.StateActive
		}
		entry := vaultcatalog.Entry{VaultID: raw.VaultID, CreatedAt: createdAt, State: state}
		if err := validateEntry(entry); err != nil {
			return nil, true, vaultcatalog.ErrCorrupt
		}
		if _, duplicate := seen[entry.VaultID]; duplicate {
			return nil, true, vaultcatalog.ErrCorrupt
		}
		seen[entry.VaultID] = struct{}{}
		entries = append(entries, entry)
	}
	return entries, true, nil
}

func (c *Catalog) store(ctx context.Context, entries []vaultcatalog.Entry, exists bool) error {
	stored := document{Version: catalogVersion, Entries: make([]documentEntry, 0, len(entries))}
	for _, entry := range entries {
		stored.Entries = append(stored.Entries, documentEntry{VaultID: entry.VaultID, CreatedAt: entry.CreatedAt.UTC().Format(time.RFC3339Nano), State: string(entry.State)})
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		return fmt.Errorf("encode Vault catalog: %w", err)
	}
	if exists {
		err = c.keys.Rotate(ctx, catalogReference, encoded)
	} else {
		err = c.keys.Put(ctx, catalogReference, encoded)
	}
	if err != nil {
		return fmt.Errorf("persist Vault catalog: %w", err)
	}
	return nil
}

func validateEntry(entry vaultcatalog.Entry) error {
	if !id.IsUUIDv7(entry.VaultID) || entry.CreatedAt.IsZero() || (entry.State != vaultcatalog.StateActive && entry.State != vaultcatalog.StatePurgePending) {
		return vaultcatalog.ErrCorrupt
	}
	return nil
}

func filterState(entries []vaultcatalog.Entry, state vaultcatalog.State) []vaultcatalog.Entry {
	filtered := make([]vaultcatalog.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.State == state {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
