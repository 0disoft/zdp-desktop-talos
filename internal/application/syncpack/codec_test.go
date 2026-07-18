package syncpack

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
)

func TestPackRoundTripBindsVaultDeviceSequenceHashAndSignature(t *testing.T) {
	t.Parallel()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{7}, 32)
	codec := New()
	events := []Event{
		{DeviceSeq: 8, EventID: "event-8", Type: "decision.answer.recorded", SchemaVersion: 1, Sensitivity: event.SensitivityPrivate, Payload: []byte(`{"answer":"first"}`), OccurredAt: "2026-07-18T08:00:00Z"},
		{DeviceSeq: 9, EventID: "event-9", Type: "memory.candidate.created", SchemaVersion: 1, Sensitivity: event.SensitivitySensitive, Payload: []byte(`{"memory":"second"}`), OccurredAt: "2026-07-18T08:01:00Z"},
	}
	encoded, exported, err := codec.Export(context.Background(), ExportInput{VaultID: "vault-1", DeviceID: "device-1", SequenceStart: 8, SequenceEnd: 9, Events: events, CreatedAt: time.Date(2026, 7, 18, 8, 2, 0, 0, time.UTC), EncryptionKey: key, SigningKey: private})
	if err != nil {
		t.Fatal(err)
	}
	manifest, imported, err := codec.Import(context.Background(), ImportInput{Encoded: encoded, VaultID: "vault-1", DeviceID: "device-1", EncryptionKey: key, VerifyKey: public})
	if err != nil || manifest.PackID != exported.PackID || len(imported) != 2 || imported[1].DeviceSeq != 9 || string(imported[1].Payload) != string(events[1].Payload) {
		t.Fatalf("manifest=%+v imported=%+v error=%v", manifest, imported, err)
	}
	if bytes.Contains(encoded, events[0].Payload) || bytes.Contains(encoded, events[1].Payload) {
		t.Fatal("plaintext event payload leaked into encoded pack")
	}
}

func TestPackRejectsTamperWrongIdentityWrongKeyAndSequenceGap(t *testing.T) {
	t.Parallel()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{3}, 32)
	wrongPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	codec := New()
	events := []Event{{DeviceSeq: 1, EventID: "event-1", Type: "task.created", SchemaVersion: 1, Sensitivity: event.SensitivityPrivate, Payload: []byte(`{"safe":true}`), OccurredAt: "2026-07-18T09:00:00Z"}}
	encoded, _, err := codec.Export(context.Background(), ExportInput{VaultID: "vault", DeviceID: "device", SequenceStart: 1, SequenceEnd: 1, Events: events, CreatedAt: time.Now().UTC(), EncryptionKey: key, SigningKey: private})
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(ImportInput) ImportInput{
		"vault":  func(input ImportInput) ImportInput { input.VaultID = "other"; return input },
		"device": func(input ImportInput) ImportInput { input.DeviceID = "other"; return input },
		"key":    func(input ImportInput) ImportInput { input.EncryptionKey = bytes.Repeat([]byte{4}, 32); return input },
		"signature": func(input ImportInput) ImportInput {
			input.VerifyKey = wrongPublic
			return input
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := mutate(ImportInput{Encoded: encoded, VaultID: "vault", DeviceID: "device", EncryptionKey: key, VerifyKey: public})
			if _, _, err := codec.Import(context.Background(), input); err == nil {
				t.Fatal("invalid pack was accepted")
			}
		})
	}
	withTrailingDocument := append(append([]byte(nil), encoded...), []byte(`{"unexpected":true}`)...)
	if _, _, err := codec.Import(context.Background(), ImportInput{Encoded: withTrailingDocument, VaultID: "vault", DeviceID: "device", EncryptionKey: key, VerifyKey: public}); !errors.Is(err, ErrInvalidPack) {
		t.Fatalf("trailing document error=%v", err)
	}
	var pack Pack
	if err := json.Unmarshal(encoded, &pack); err != nil {
		t.Fatal(err)
	}
	pack.Ciphertext[0] ^= 0xff
	tampered, _ := json.Marshal(pack)
	if _, _, err := codec.Import(context.Background(), ImportInput{Encoded: tampered, VaultID: "vault", DeviceID: "device", EncryptionKey: key, VerifyKey: public}); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("tamper error=%v", err)
	}
	gap := append([]Event(nil), events...)
	gap[0].DeviceSeq = 2
	if _, _, err := codec.Export(context.Background(), ExportInput{VaultID: "vault", DeviceID: "device", SequenceStart: 1, SequenceEnd: 1, Events: gap, CreatedAt: time.Now().UTC(), EncryptionKey: key, SigningKey: private}); !errors.Is(err, ErrSequenceInvalid) {
		t.Fatalf("gap error=%v", err)
	}
}
