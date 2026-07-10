package dpapikeyvault

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
)

const (
	recordMagic        = "TALOSDP1"
	recordHeaderSize   = len(recordMagic) + 4
	maxSecretSize      = 64 * 1024
	maxProtectedSize   = 128 * 1024
	maxReferenceLength = 128
)

type Store struct {
	root string
	mu   sync.Mutex
}

func Open(root string) (*Store, error) {
	if !platformAvailable() {
		return nil, keyvault.ErrUnsupported
	}
	if root == "" {
		return nil, fmt.Errorf("%w: storage root is empty", keyvault.ErrInvalidReference)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve key vault root: %w", err)
	}
	return &Store{root: filepath.Clean(absRoot)}, nil
}

func (s *Store) Put(ctx context.Context, ref keyvault.Reference, secret []byte) error {
	return s.write(ctx, ref, secret, false)
}

func (s *Store) Rotate(ctx context.Context, ref keyvault.Reference, secret []byte) error {
	return s.write(ctx, ref, secret, true)
}

func (s *Store) Get(ctx context.Context, ref keyvault.Reference) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	canonical, err := canonicalReference(ref)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := readBounded(s.pathFor(canonical))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, keyvault.ErrNotFound
		}
		return nil, fmt.Errorf("read protected key record: %w", err)
	}
	protected, err := decodeRecord(record)
	if err != nil {
		return nil, err
	}
	secret, err := platformUnprotect(protected, entropy(canonical))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", keyvault.ErrUnseal, err)
	}
	return secret, nil
}

func (s *Store) Delete(ctx context.Context, ref keyvault.Reference) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	canonical, err := canonicalReference(ref)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Remove(s.pathFor(canonical)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return keyvault.ErrNotFound
		}
		return fmt.Errorf("delete protected key record: %w", err)
	}
	return nil
}

func (s *Store) write(ctx context.Context, ref keyvault.Reference, secret []byte, replace bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	canonical, err := canonicalReference(ref)
	if err != nil {
		return err
	}
	if len(secret) == 0 || len(secret) > maxSecretSize {
		return fmt.Errorf("%w: secret length must be between 1 and %d bytes", keyvault.ErrInvalidSecret, maxSecretSize)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	target := s.pathFor(canonical)
	if replace {
		if _, err := os.Stat(target); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return keyvault.ErrNotFound
			}
			return fmt.Errorf("inspect protected key record: %w", err)
		}
	} else if _, err := os.Stat(target); err == nil {
		return keyvault.ErrAlreadyExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect protected key record: %w", err)
	}

	protected, err := platformProtect(secret, entropy(canonical))
	if err != nil {
		return fmt.Errorf("protect key record: %w", err)
	}
	record, err := encodeRecord(protected)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeAtomic(s.root, target, record, replace); err != nil {
		if !replace && errors.Is(err, os.ErrExist) {
			return keyvault.ErrAlreadyExists
		}
		if replace && errors.Is(err, os.ErrNotExist) {
			return keyvault.ErrNotFound
		}
		return fmt.Errorf("commit protected key record: %w", err)
	}
	return nil
}

func canonicalReference(ref keyvault.Reference) (string, error) {
	if !validReferencePart(ref.VaultID) || !validReferencePart(ref.KeyID) {
		return "", keyvault.ErrInvalidReference
	}
	return ref.VaultID + "\x00" + ref.KeyID, nil
}

func validReferencePart(value string) bool {
	if len(value) == 0 || len(value) > maxReferenceLength {
		return false
	}
	for index, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' {
			continue
		}
		if index > 0 && (char == '-' || char == '_' || char == '.') {
			continue
		}
		return false
	}
	return true
}

func (s *Store) pathFor(canonical string) string {
	hash := sha256.Sum256([]byte(canonical))
	return filepath.Join(s.root, fmt.Sprintf("%x.dpapi", hash[:]))
}

func entropy(canonical string) []byte {
	hash := sha256.Sum256([]byte("talos.dpapi-keyvault/v1\x00" + canonical))
	return hash[:]
}

func encodeRecord(protected []byte) ([]byte, error) {
	if len(protected) == 0 || len(protected) > maxProtectedSize {
		return nil, keyvault.ErrCorrupt
	}
	record := make([]byte, recordHeaderSize+len(protected))
	copy(record, recordMagic)
	binary.LittleEndian.PutUint32(record[len(recordMagic):recordHeaderSize], uint32(len(protected)))
	copy(record[recordHeaderSize:], protected)
	return record, nil
}

func decodeRecord(record []byte) ([]byte, error) {
	if len(record) < recordHeaderSize || !bytes.Equal(record[:len(recordMagic)], []byte(recordMagic)) {
		return nil, keyvault.ErrCorrupt
	}
	protectedSize := int(binary.LittleEndian.Uint32(record[len(recordMagic):recordHeaderSize]))
	if protectedSize == 0 || protectedSize > maxProtectedSize || len(record) != recordHeaderSize+protectedSize {
		return nil, keyvault.ErrCorrupt
	}
	return append([]byte(nil), record[recordHeaderSize:]...), nil
}

func readBounded(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := io.LimitReader(file, int64(maxProtectedSize+recordHeaderSize+1))
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if len(data) > maxProtectedSize+recordHeaderSize {
		return nil, keyvault.ErrCorrupt
	}
	return data, nil
}

func writeAtomic(root, target string, data []byte, replace bool) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(root, ".talos-key-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return platformCommit(temporaryPath, target, replace)
}
