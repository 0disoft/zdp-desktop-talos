package wailsapi

import (
	"errors"
	"strings"
	"testing"

	"github.com/0disoft/zdp-desktop-talos/internal/application/patchcommand"
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

func TestMapErrorUsesStablePatchCommandCodes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  error
		code string
	}{
		{patchcommand.ErrStaleReview, "PATCH_REVIEW_STALE"},
		{patchcommand.ErrSecretFindings, "PATCH_SECRET_FINDINGS"},
		{patchcommand.ErrUnscannableChanges, "PATCH_UNSCANNABLE_CHANGES"},
		{patchcommand.ErrScopeViolation, "PATCH_SCOPE_VIOLATION"},
		{patchcommand.ErrPatchConflict, "PATCH_CONFLICT"},
		{patchcommand.ErrPatchOutcomeUnknown, "PATCH_OUTCOME_UNKNOWN"},
	}
	for _, item := range tests {
		mapped := MapError(errors.Join(item.err, errors.New(`C:\private\repository`)), "patch-command")
		if mapped.Code != item.code || strings.Contains(mapped.Message, "private") {
			t.Fatalf("error=%v mapped=%+v", item.err, mapped)
		}
	}
}

func TestMapErrorDoesNotReflectUnboundedCorrelationID(t *testing.T) {
	t.Parallel()
	for _, value := range []string{strings.Repeat("a", 129), "request\nforged", "요청"} {
		if mapped := MapError(errors.New("failed"), value); mapped.CorrelationID != "" {
			t.Fatalf("unsafe correlation ID was reflected: %q", mapped.CorrelationID)
		}
	}
}
