package event

import (
	"errors"
	"testing"
	"time"
)

func TestSecretPayloadIsRejected(t *testing.T) {
	t.Parallel()

	record := Record{
		ID:            "0190a57e-6d90-7b34-8d42-f151641bd28e",
		VaultID:       "vault-test",
		Type:          "secret.detected",
		SchemaVersion: 1,
		Sensitivity:   SensitivitySecret,
		Payload:       []byte("must-not-persist"),
		OccurredAt:    time.Now().UTC(),
	}

	if err := record.Validate(); !errors.Is(err, ErrSecretPayload) {
		t.Fatalf("expected ErrSecretPayload, got %v", err)
	}
}

func TestKnownSensitivitiesValidate(t *testing.T) {
	t.Parallel()

	for _, sensitivity := range []Sensitivity{
		SensitivityPublic,
		SensitivityPrivate,
		SensitivitySensitive,
		SensitivitySecret,
	} {
		if err := sensitivity.Validate(); err != nil {
			t.Fatalf("expected %q to validate: %v", sensitivity, err)
		}
	}
}
