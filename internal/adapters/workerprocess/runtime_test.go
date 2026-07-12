package workerprocess

import (
	"errors"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/ports/workerruntime"
	"github.com/0disoft/zdp-desktop-talos/internal/workeripc"
)

func TestMapResultAcceptsKnownSuccessfulResult(t *testing.T) {
	started := time.Date(2026, time.July, 12, 1, 2, 3, 0, time.UTC)
	finished := started.Add(time.Second)

	result, err := mapResult(workeripc.ToolResultPayload{
		State:      "succeeded",
		ExitCode:   0,
		Stdout:     []byte("ok"),
		StartedAt:  started.Format(time.RFC3339Nano),
		FinishedAt: finished.Format(time.RFC3339Nano),
	}, nil)
	if err != nil {
		t.Fatalf("map result: %v", err)
	}
	if result.State != workerruntime.ToolSucceeded || !result.StartedAt.Equal(started) || !result.FinishedAt.Equal(finished) {
		t.Fatalf("unexpected mapped result: %+v", result)
	}
}

func TestMapResultRejectsUnknownState(t *testing.T) {
	_, err := mapResult(workeripc.ToolResultPayload{State: "future_state"}, nil)
	if !errors.Is(err, workerruntime.ErrProtocol) {
		t.Fatalf("expected protocol error, got %v", err)
	}
}

func TestMapResultRejectsInvalidTimestamp(t *testing.T) {
	_, err := mapResult(workeripc.ToolResultPayload{State: "succeeded", StartedAt: "not-a-time"}, nil)
	if !errors.Is(err, workerruntime.ErrProtocol) {
		t.Fatalf("expected protocol error, got %v", err)
	}
}

func TestMapResultPreservesKnownFailure(t *testing.T) {
	result, err := mapResult(workeripc.ToolResultPayload{State: "timed_out", ExitCode: -1}, nil)
	if !errors.Is(err, workerruntime.ErrExecutionFailed) {
		t.Fatalf("expected execution failure, got %v", err)
	}
	if result.State != workerruntime.ToolTimedOut || result.ExitCode != -1 {
		t.Fatalf("unexpected mapped failure: %+v", result)
	}
}
