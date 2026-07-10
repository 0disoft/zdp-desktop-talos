package wailsapi

import (
	"errors"
	"strings"
	"testing"

	"github.com/0disoft/zdp-desktop-talos/internal/security/envelope"
)

func TestMapErrorDoesNotExposeInternalErrorText(t *testing.T) {
	t.Parallel()

	internal := errors.Join(envelope.ErrInvalidEnvelope, errors.New("C:\\Users\\private\\vault.db"))
	mapped := MapError(internal, "correlation-1")
	if mapped.Code != "ENCRYPTED_PAYLOAD_INVALID" {
		t.Fatalf("unexpected code %q", mapped.Code)
	}
	if strings.Contains(mapped.Message, "private") || strings.Contains(mapped.Message, "vault.db") {
		t.Fatalf("mapped message leaked internal detail: %q", mapped.Message)
	}
	if mapped.CorrelationID != "correlation-1" {
		t.Fatalf("unexpected correlation id %q", mapped.CorrelationID)
	}
}
