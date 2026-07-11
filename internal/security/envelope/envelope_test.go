package envelope

import (
	"bytes"
	"errors"
	"testing"
)

func TestSealRoundTripAndTamperDetection(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{0x42}, keySize)
	sealer, err := NewSealer("test-key", key)
	if err != nil {
		t.Fatalf("NewSealer returned error: %v", err)
	}
	metadata := AAD{VaultID: "vault", ObjectID: "event", SchemaVersion: 1, Sensitivity: "private"}

	encoded, err := sealer.Seal([]byte("private marker"), metadata)
	if err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	if bytes.Contains(encoded, []byte("private marker")) {
		t.Fatal("ciphertext contains plaintext marker")
	}

	plaintext, err := sealer.Open(encoded, metadata)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if string(plaintext) != "private marker" {
		t.Fatalf("unexpected plaintext %q", plaintext)
	}

	encoded[len(encoded)-2] ^= 0x01
	if _, err := sealer.Open(encoded, metadata); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("expected tamper error, got %v", err)
	}
}

func TestWrongAADAndWrongKeyFailClosed(t *testing.T) {
	t.Parallel()

	sealer, err := NewSealer("test-key", bytes.Repeat([]byte{0x21}, keySize))
	if err != nil {
		t.Fatalf("NewSealer returned error: %v", err)
	}
	metadata := AAD{VaultID: "vault", ObjectID: "event", SchemaVersion: 1, Sensitivity: "private"}
	encoded, err := sealer.Seal([]byte("payload"), metadata)
	if err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}

	wrongAAD := metadata
	wrongAAD.ObjectID = "other-event"
	if _, err := sealer.Open(encoded, wrongAAD); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("expected AAD mismatch error, got %v", err)
	}

	wrongKey, err := NewSealer("test-key", bytes.Repeat([]byte{0x22}, keySize))
	if err != nil {
		t.Fatalf("NewSealer returned error: %v", err)
	}
	if _, err := wrongKey.Open(encoded, metadata); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("expected wrong-key error, got %v", err)
	}
}

func TestDestroyMakesSealerUnableToDecryptPreviousPayload(t *testing.T) {
	t.Parallel()
	key := bytes.Repeat([]byte{7}, keySize)
	sealer, err := NewSealer("key-1", key)
	if err != nil {
		t.Fatal(err)
	}
	metadata := AAD{VaultID: "vault-1", ObjectID: "event-1", SchemaVersion: 1, Sensitivity: "private"}
	ciphertext, err := sealer.Seal([]byte("private"), metadata)
	if err != nil {
		t.Fatal(err)
	}
	sealer.Destroy()
	if _, err := sealer.Open(ciphertext, metadata); !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("destroyed sealer decrypted payload: %v", err)
	}
}
