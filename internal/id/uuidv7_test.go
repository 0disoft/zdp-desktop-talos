package id

import (
	"bytes"
	"testing"
	"time"
)

func TestUUIDv7EncodesVersionAndVariant(t *testing.T) {
	t.Parallel()

	value, err := UUIDv7(time.UnixMilli(1_720_000_000_000), bytes.NewReader(make([]byte, 16)))
	if err != nil {
		t.Fatalf("UUIDv7 returned error: %v", err)
	}
	if len(value) != 36 {
		t.Fatalf("expected 36-character UUID, got %q", value)
	}
	if value[14] != '7' {
		t.Fatalf("expected version 7 UUID, got %q", value)
	}
	if value[19] != '8' {
		t.Fatalf("expected RFC 4122 variant, got %q", value)
	}
}
