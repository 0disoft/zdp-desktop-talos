package syncpack

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/syncstate"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/syncstore"
)

func TestImporterUsesMembershipKeyAndRecordsOnlyVerifiedPack(t *testing.T) {
	t.Parallel()
	trustedPublic, trustedPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, untrustedPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{9}, 32)
	codec := New()
	events := []Event{{DeviceSeq: 1, EventID: "remote-event-1", Type: "memory.candidate.created", SchemaVersion: 1, Sensitivity: event.SensitivityPrivate, Payload: []byte(`{"private":true}`), OccurredAt: "2026-07-18T11:00:00Z"}}
	export := func(signingKey ed25519.PrivateKey) []byte {
		encoded, _, exportErr := codec.Export(context.Background(), ExportInput{VaultID: "vault", DeviceID: "device", SequenceStart: 1, SequenceEnd: 1, Events: events, CreatedAt: time.Date(2026, 7, 18, 11, 1, 0, 0, time.UTC), EncryptionKey: key, SigningKey: signingKey})
		if exportErr != nil {
			t.Fatal(exportErr)
		}
		return encoded
	}
	store := &importStore{device: syncstate.Device{VaultID: "vault", DeviceID: "device", PublicKey: trustedPublic, State: syncstate.DeviceActive, Revision: 1, NextSequence: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), LastEventID: "device-event"}}
	importer, err := NewImporter(codec, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := importer.Prepare(context.Background(), PrepareImportInput{Encoded: export(untrustedPrivate), VaultID: "vault", DeviceID: "device", EncryptionKey: key}); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("untrusted signer error=%v", err)
	}
	if store.recordCalls != 0 {
		t.Fatal("unverified pack reached the import journal")
	}
	result, err := importer.Prepare(context.Background(), PrepareImportInput{Encoded: export(trustedPrivate), VaultID: "vault", DeviceID: "device", EncryptionKey: key, ReceivedAt: time.Date(2026, 7, 18, 11, 2, 0, 0, time.UTC)})
	if err != nil || result.Receipt.PackID == "" || len(result.Events) != 1 || store.recordCalls != 1 {
		t.Fatalf("result=%+v calls=%d error=%v", result, store.recordCalls, err)
	}
}

type importStore struct {
	device      syncstate.Device
	recordCalls int
}

func (s *importStore) GetSyncDevice(context.Context, string, string) (syncstate.Device, error) {
	return s.device, nil
}

func (s *importStore) RecordValidatedSyncPack(_ context.Context, input syncstore.RecordPackInput) (syncstate.PackReceipt, bool, error) {
	s.recordCalls++
	return syncstate.PackReceipt{PackID: input.PackID, VaultID: input.VaultID, DeviceID: input.DeviceID, SequenceStart: input.SequenceStart, SequenceEnd: input.SequenceEnd, EventCount: input.EventCount, CiphertextHash: input.CiphertextHash, State: syncstate.PackValidated, ReceivedAt: input.ReceivedAt, LastEventID: "pack-event"}, false, nil
}

func (s *importStore) RegisterSyncDevice(context.Context, syncstore.RegisterDeviceInput) (syncstate.Device, error) {
	panic("not used")
}

func (s *importStore) RevokeSyncDevice(context.Context, syncstore.RevokeDeviceInput) (syncstate.Device, error) {
	panic("not used")
}

func (s *importStore) GetValidatedSyncPack(context.Context, string, string) (syncstate.PackReceipt, []byte, error) {
	panic("not used")
}

func (s *importStore) PrepareSyncExport(context.Context, syncstore.PrepareExportInput) (syncstore.PreparedExport, bool, error) {
	panic("not used")
}

func (s *importStore) FinalizeSyncExport(context.Context, syncstore.FinalizeExportInput) (syncstate.ExportBatch, []byte, bool, error) {
	panic("not used")
}

func (s *importStore) GetSyncExport(context.Context, string, string) (syncstate.ExportBatch, []byte, error) {
	panic("not used")
}
