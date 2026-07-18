package syncpack

import (
	"context"
	"errors"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/syncstate"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/syncstore"
)

var ErrDeviceNotTrusted = errors.New("sync pack device is not trusted")

type PrepareImportInput struct {
	Encoded       []byte
	VaultID       string
	DeviceID      string
	EncryptionKey []byte
	ReceivedAt    time.Time
}

type PrepareImportResult struct {
	Manifest Manifest
	Events   []Event
	Receipt  syncstate.PackReceipt
	Replay   bool
}

type Importer struct {
	codec *Codec
	store syncstore.Store
	now   func() time.Time
}

func NewImporter(codec *Codec, store syncstore.Store) (*Importer, error) {
	if codec == nil || store == nil {
		return nil, ErrInvalidPack
	}
	return &Importer{codec: codec, store: store, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (i *Importer) Prepare(ctx context.Context, input PrepareImportInput) (PrepareImportResult, error) {
	if i == nil || i.codec == nil || i.store == nil || ctx == nil {
		return PrepareImportResult{}, ErrInvalidPack
	}
	device, err := i.store.GetSyncDevice(ctx, input.VaultID, input.DeviceID)
	if err != nil {
		return PrepareImportResult{}, err
	}
	if device.State != syncstate.DeviceActive {
		return PrepareImportResult{}, ErrDeviceNotTrusted
	}
	manifest, events, err := i.codec.Import(ctx, ImportInput{Encoded: input.Encoded, VaultID: input.VaultID, DeviceID: input.DeviceID, EncryptionKey: input.EncryptionKey, VerifyKey: device.PublicKey})
	if err != nil {
		return PrepareImportResult{}, err
	}
	if input.ReceivedAt.IsZero() {
		input.ReceivedAt = i.now()
	}
	receipt, replay, err := i.store.RecordValidatedSyncPack(ctx, syncstore.RecordPackInput{PackID: manifest.PackID, VaultID: manifest.VaultID, DeviceID: manifest.DeviceID, SequenceStart: manifest.SequenceStart, SequenceEnd: manifest.SequenceEnd, EventCount: manifest.EventCount, CiphertextHash: manifest.CiphertextHash, EncodedPack: input.Encoded, ReceivedAt: input.ReceivedAt})
	if err != nil {
		return PrepareImportResult{}, err
	}
	return PrepareImportResult{Manifest: manifest, Events: events, Receipt: receipt, Replay: replay}, nil
}
