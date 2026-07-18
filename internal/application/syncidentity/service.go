package syncidentity

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/syncstate"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/syncstore"
)

const SigningKeyID = "sync-device-ed25519-v1"

var (
	ErrInvalidRequest = errors.New("invalid sync identity request")
	ErrKeyInvalid     = errors.New("sync device signing key is invalid")
	ErrDeviceRevoked  = errors.New("local sync device is revoked")
)

type Identity struct {
	Device     syncstate.Device
	PrivateKey ed25519.PrivateKey
}

type Service struct {
	keys   keyvault.Store
	store  syncstore.Store
	random io.Reader
	now    func() time.Time
}

func New(keys keyvault.Store, store syncstore.Store) (*Service, error) {
	if keys == nil || store == nil {
		return nil, ErrInvalidRequest
	}
	return &Service{keys: keys, store: store, random: rand.Reader, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *Service) Ensure(ctx context.Context, vaultID string) (Identity, error) {
	if s == nil || s.keys == nil || s.store == nil || ctx == nil || strings.TrimSpace(vaultID) == "" {
		return Identity{}, ErrInvalidRequest
	}
	privateKey, err := s.loadOrCreate(ctx, vaultID)
	if err != nil {
		return Identity{}, err
	}
	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		clear(privateKey)
		return Identity{}, ErrKeyInvalid
	}
	deviceID := DeviceID(publicKey)
	device, err := s.store.GetSyncDevice(ctx, vaultID, deviceID)
	if errors.Is(err, syncstore.ErrDeviceNotFound) {
		device, err = s.store.RegisterSyncDevice(ctx, syncstore.RegisterDeviceInput{
			VaultID: vaultID, DeviceID: deviceID, PublicKey: publicKey, OccurredAt: s.now(),
			IdempotencyKey: "sync-local-device:" + deviceID,
		})
	}
	if err != nil {
		clear(privateKey)
		return Identity{}, err
	}
	if device.State != syncstate.DeviceActive {
		clear(privateKey)
		return Identity{}, ErrDeviceRevoked
	}
	if !bytes.Equal(device.PublicKey, publicKey) {
		clear(privateKey)
		return Identity{}, ErrKeyInvalid
	}
	return Identity{Device: device, PrivateKey: privateKey}, nil
}

func (s *Service) loadOrCreate(ctx context.Context, vaultID string) (ed25519.PrivateKey, error) {
	ref := keyvault.Reference{VaultID: vaultID, KeyID: SigningKeyID}
	stored, err := s.keys.Get(ctx, ref)
	if err == nil {
		return validatePrivateKey(stored)
	}
	if !errors.Is(err, keyvault.ErrNotFound) {
		return nil, err
	}
	_, generated, err := ed25519.GenerateKey(s.random)
	if err != nil {
		return nil, fmt.Errorf("generate sync signing key: %w", err)
	}
	if err := s.keys.Put(ctx, ref, generated); err != nil {
		clear(generated)
		if !errors.Is(err, keyvault.ErrAlreadyExists) {
			return nil, err
		}
		stored, err = s.keys.Get(ctx, ref)
		if err != nil {
			return nil, err
		}
		return validatePrivateKey(stored)
	}
	return append(ed25519.PrivateKey(nil), generated...), nil
}

func validatePrivateKey(value []byte) (ed25519.PrivateKey, error) {
	defer clear(value)
	if len(value) != ed25519.PrivateKeySize {
		return nil, ErrKeyInvalid
	}
	privateKey := append(ed25519.PrivateKey(nil), value...)
	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize || !bytes.Equal(privateKey[32:], publicKey) {
		clear(privateKey)
		return nil, ErrKeyInvalid
	}
	return privateKey, nil
}

func DeviceID(publicKey ed25519.PublicKey) string {
	hash := sha256.Sum256(publicKey)
	return "device-v1-" + hex.EncodeToString(hash[:])
}
