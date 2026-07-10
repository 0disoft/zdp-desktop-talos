package workeripc

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

type oneByteReader struct {
	reader io.Reader
}

func (r oneByteReader) Read(buffer []byte) (int, error) {
	if len(buffer) > 1 {
		buffer = buffer[:1]
	}
	return r.reader.Read(buffer)
}

func TestFrameRoundTripWithPartialReads(t *testing.T) {
	t.Parallel()

	var buffer bytes.Buffer
	encoder := NewEncoder(&buffer, MaxFrameSize)
	want := Message{Version: ProtocolVersion, Type: "heartbeat", RequestID: "request-1"}
	if err := encoder.Write(want); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	decoder := NewDecoder(oneByteReader{reader: &buffer}, MaxFrameSize)
	got, err := decoder.Read()
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if got.Version != want.Version || got.Type != want.Type || got.RequestID != want.RequestID {
		t.Fatalf("unexpected message: %#v", got)
	}
}

func TestMalformedAndOversizedFramesAreRejected(t *testing.T) {
	t.Parallel()

	malformed := NewDecoder(strings.NewReader("Content-Length: nope\r\n\r\n"), MaxFrameSize)
	if _, err := malformed.Read(); !errors.Is(err, ErrMalformedFrame) {
		t.Fatalf("expected malformed frame error, got %v", err)
	}

	oversized := NewDecoder(strings.NewReader("Content-Length: 9\r\n\r\n"), 8)
	if _, err := oversized.Read(); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("expected frame-too-large error, got %v", err)
	}
}

func TestServerRejectsProtocolVersionMismatch(t *testing.T) {
	t.Parallel()

	var input bytes.Buffer
	if err := NewEncoder(&input, MaxFrameSize).Write(Message{Version: ProtocolVersion + 1, Type: "handshake", RequestID: "request-1"}); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	var output bytes.Buffer
	err := Serve(&input, &output)
	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("expected version mismatch, got %v", err)
	}
	response, err := NewDecoder(&output, MaxFrameSize).Read()
	if err != nil {
		t.Fatalf("read error response: %v", err)
	}
	if response.Error == nil || response.Error.Code != "WORKER_PROTOCOL_VERSION_MISMATCH" {
		t.Fatalf("unexpected error response: %#v", response)
	}
}
