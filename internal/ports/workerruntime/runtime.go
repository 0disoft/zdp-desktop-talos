package workerruntime

import (
	"context"
	"errors"
	"time"
)

var (
	ErrUnavailable     = errors.New("worker runtime is unavailable")
	ErrProtocol        = errors.New("worker runtime protocol failed")
	ErrExecutionFailed = errors.New("worker tool execution failed")
)

type ToolState string

const (
	ToolSucceeded     ToolState = "succeeded"
	ToolFailed        ToolState = "failed"
	ToolCanceled      ToolState = "canceled"
	ToolTimedOut      ToolState = "timed_out"
	ToolOutputLimited ToolState = "output_limited"
	ToolUnknown       ToolState = "unknown"
)

type Capability struct {
	ID               string
	Executable       string
	Arguments        []string
	EnvironmentNames []string
	Timeout          time.Duration
	MaxOutputBytes   int
}

type RunPolicy struct {
	RunID        string
	WorktreeRoot string
	Capabilities []Capability
}

type ToolRequest struct {
	RunID            string
	CallID           string
	CapabilityID     string
	Arguments        []string
	WorkingDirectory string
	Environment      map[string]string
	Timeout          time.Duration
	MaxOutputBytes   int
}

type ToolResult struct {
	State        ToolState
	ExitCode     int
	StdoutBytes  int
	StderrBytes  int
	StdoutSHA256 string
	StderrSHA256 string
	StartedAt    time.Time
	FinishedAt   time.Time
}

type Session interface {
	StartRun(context.Context, RunPolicy) error
	RunTool(context.Context, ToolRequest) (ToolResult, error)
	Shutdown(context.Context) error
	Close() error
}

type Factory interface {
	Start(context.Context) (Session, error)
}
