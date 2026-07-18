package syncexport

import (
	"context"
	"errors"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/application/syncidentity"
	"github.com/0disoft/zdp-desktop-talos/internal/application/syncpack"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/syncstate"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/secretscanner"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/syncstore"
)

var (
	ErrInvalidRequest = errors.New("invalid sync export request")
	ErrSecretFindings = errors.New("sync export contains secret findings")
)

type Result struct {
	Batch    syncstate.ExportBatch
	Manifest syncpack.Manifest
	Encoded  []byte
	Replay   bool
}

type Service struct {
	codec      *syncpack.Codec
	identity   *syncidentity.Service
	store      syncstore.Store
	keys       keyvault.Store
	scanner    secretscanner.Scanner
	vaultKeyID string
	now        func() time.Time
}

func New(codec *syncpack.Codec, store syncstore.Store, keys keyvault.Store, scanner secretscanner.Scanner, vaultKeyID string) (*Service, error) {
	if codec == nil || store == nil || keys == nil || scanner == nil || vaultKeyID == "" {
		return nil, ErrInvalidRequest
	}
	identity, err := syncidentity.New(keys, store)
	if err != nil {
		return nil, err
	}
	return &Service{codec: codec, identity: identity, store: store, keys: keys, scanner: scanner, vaultKeyID: vaultKeyID, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *Service) ExportNext(ctx context.Context, vaultID string, limit int) (Result, error) {
	if s == nil || ctx == nil || vaultID == "" || limit < 1 || limit > syncpack.MaxEventsPerPack {
		return Result{}, ErrInvalidRequest
	}
	identity, err := s.identity.Current(ctx, vaultID)
	if err != nil {
		return Result{}, err
	}
	defer clear(identity.PrivateKey)
	vaultKey, err := s.keys.Get(ctx, keyvault.Reference{VaultID: vaultID, KeyID: s.vaultKeyID})
	if err != nil {
		return Result{}, err
	}
	defer clear(vaultKey)
	prepared, reservationReplay, err := s.store.PrepareSyncExport(ctx, syncstore.PrepareExportInput{VaultID: vaultID, DeviceID: identity.Device.DeviceID, Limit: limit, OccurredAt: s.now()})
	if err != nil {
		return Result{}, err
	}
	events := make([]syncpack.Event, len(prepared.Events))
	for index, record := range prepared.Events {
		scan, err := s.scanner.Redact(ctx, string(record.Payload))
		if err != nil {
			clear(record.Payload)
			return Result{}, err
		}
		if scan.Findings > 0 {
			clear(record.Payload)
			return Result{}, ErrSecretFindings
		}
		events[index] = syncpack.Event{DeviceSeq: prepared.Batch.SequenceStart + uint64(index), EventID: record.ID, Type: record.Type, SchemaVersion: record.SchemaVersion, Sensitivity: record.Sensitivity, Payload: append([]byte(nil), record.Payload...), OccurredAt: record.OccurredAt.UTC().Format(time.RFC3339Nano)}
		defer clear(events[index].Payload)
		clear(record.Payload)
	}
	encoded, manifest, err := s.codec.Export(ctx, syncpack.ExportInput{VaultID: vaultID, DeviceID: identity.Device.DeviceID, SequenceStart: prepared.Batch.SequenceStart, SequenceEnd: prepared.Batch.SequenceEnd, Events: events, CreatedAt: prepared.Batch.CreatedAt, EncryptionKey: vaultKey, SigningKey: identity.PrivateKey})
	if err != nil {
		return Result{}, err
	}
	batch, stored, finalizeReplay, err := s.store.FinalizeSyncExport(ctx, syncstore.FinalizeExportInput{ExportID: prepared.Batch.ExportID, VaultID: vaultID, DeviceID: identity.Device.DeviceID, PackID: manifest.PackID, SequenceStart: manifest.SequenceStart, SequenceEnd: manifest.SequenceEnd, EventCount: manifest.EventCount, CiphertextHash: manifest.CiphertextHash, EncodedPack: encoded, OccurredAt: s.now()})
	if err != nil {
		return Result{}, err
	}
	if finalizeReplay {
		manifest, _, err = s.codec.Import(ctx, syncpack.ImportInput{Encoded: stored, VaultID: vaultID, DeviceID: identity.Device.DeviceID, EncryptionKey: vaultKey, VerifyKey: identity.Device.PublicKey})
		if err != nil {
			return Result{}, err
		}
	}
	return Result{Batch: batch, Manifest: manifest, Encoded: stored, Replay: reservationReplay || finalizeReplay}, nil
}
