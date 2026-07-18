package backupstream

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestChunkedRoundTripDoesNotExposePlaintext(t *testing.T) {
	t.Parallel()
	key := bytes.Repeat([]byte{0x4a}, keySize)
	payload := bytes.Repeat([]byte("private-backup-marker/"), ChunkSize/8)
	payload = append(payload, bytes.Repeat([]byte{0x7f}, ChunkSize+37)...)

	var encrypted bytes.Buffer
	writer, err := NewWriter(&encrypted, "vault-kek-v1", key)
	if err != nil {
		t.Fatal(err)
	}
	for offset := 0; offset < len(payload); {
		end := offset + 333_333
		if end > len(payload) {
			end = len(payload)
		}
		if _, err := writer.Write(payload[offset:end]); err != nil {
			t.Fatal(err)
		}
		offset = end
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted.Bytes(), []byte("private-backup-marker")) {
		t.Fatal("encrypted stream contains plaintext marker")
	}

	reader, err := NewReader(bytes.NewReader(encrypted.Bytes()), "vault-kek-v1", key)
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, payload) {
		t.Fatalf("round trip changed %d bytes", len(payload))
	}
}

func TestWrongKeyTamperTruncationAndTrailingBytesFailClosed(t *testing.T) {
	t.Parallel()
	key := bytes.Repeat([]byte{0x61}, keySize)
	encoded := encryptedFixture(t, key, bytes.Repeat([]byte("payload"), 200_000))

	tests := []struct {
		name string
		key  []byte
		edit func([]byte) []byte
	}{
		{name: "wrong key", key: bytes.Repeat([]byte{0x62}, keySize), edit: func(value []byte) []byte { return value }},
		{name: "tamper", key: key, edit: func(value []byte) []byte { value[len(value)/2] ^= 0x80; return value }},
		{name: "truncated", key: key, edit: func(value []byte) []byte { return value[:len(value)-1] }},
		{name: "trailing", key: key, edit: func(value []byte) []byte { return append(value, 0x00) }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := test.edit(append([]byte(nil), encoded...))
			reader, err := NewReader(bytes.NewReader(candidate), "vault-kek-v1", test.key)
			if err == nil {
				_, err = io.ReadAll(reader)
				_ = reader.Close()
			}
			if !errors.Is(err, ErrInvalidStream) {
				t.Fatalf("error = %v, want ErrInvalidStream", err)
			}
		})
	}
}

func TestReaderRejectsUnexpectedKeyIDAndMissingFinalFrame(t *testing.T) {
	t.Parallel()
	key := bytes.Repeat([]byte{0x31}, keySize)
	encoded := encryptedFixture(t, key, []byte("small payload"))
	if _, err := NewReader(bytes.NewReader(encoded), "other-key", key); !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("unexpected key id error = %v", err)
	}

	finalFrameBytes := frameHeaderSize + 16
	reader, err := NewReader(bytes.NewReader(encoded[:len(encoded)-finalFrameBytes]), "vault-kek-v1", key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(reader); !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("missing final frame error = %v", err)
	}
}

func encryptedFixture(t *testing.T, key, payload []byte) []byte {
	t.Helper()
	var encoded bytes.Buffer
	writer, err := NewWriter(&encoded, "vault-kek-v1", key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}
