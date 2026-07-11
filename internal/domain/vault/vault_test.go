package vault

import (
	"errors"
	"testing"
	"time"
)

func TestRecordValidation(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_720_000_000, 0).UTC()
	valid := Record{
		ID:            "vault-test",
		Revision:      1,
		Status:        StatusActive,
		RetentionDays: 30,
		CreatedAt:     now,
		UpdatedAt:     now,
		LastEventID:   "event-test",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid record rejected: %v", err)
	}

	invalid := valid
	invalid.RetentionDays = 0
	if !errors.Is(invalid.Validate(), ErrInvalidRecord) {
		t.Fatalf("invalid retention was accepted: %v", invalid.Validate())
	}
	invalid = valid
	invalid.UpdatedAt = now.Add(-time.Second)
	if !errors.Is(invalid.Validate(), ErrInvalidRecord) {
		t.Fatalf("reversed timestamps were accepted: %v", invalid.Validate())
	}
}
