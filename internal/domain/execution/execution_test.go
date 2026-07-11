package execution

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAttemptValidationKeepsUnknownDistinctFromFailure(t *testing.T) {
	now := time.Now().UTC()
	base := Attempt{ID: "attempt-1", RunID: "run-1", CallID: "call-1", CapabilityHash: strings.Repeat("a", 64), CreatedAt: now, UpdatedAt: now, LastEventID: "event-1"}
	unknown := base
	unknown.State = AttemptUnknown
	unknown.SafeErrorCode = "WORKER_OUTCOME_UNKNOWN"
	if err := unknown.Validate(); err != nil {
		t.Fatal(err)
	}
	unknown.SafeErrorCode = ""
	if err := unknown.Validate(); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("unknown without reason error=%v", err)
	}
	failed := base
	failed.State = AttemptFailed
	code := 1
	failed.ExitCode = &code
	if err := failed.Validate(); err != nil {
		t.Fatal(err)
	}
}
