package workerprocess

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/ports/workerruntime"
	"github.com/0disoft/zdp-desktop-talos/internal/workeripc"
)

const cancelTimeout = 5 * time.Second

type Factory struct{ executable string }

func New(executable string) (*Factory, error) {
	if executable == "" {
		return nil, workerruntime.ErrUnavailable
	}
	return &Factory{executable: executable}, nil
}

func (f *Factory) Start(ctx context.Context) (workerruntime.Session, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, workerruntime.ErrUnavailable
	}
	client, err := workeripc.StartSession(f.executable)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", workerruntime.ErrUnavailable, err)
	}
	handshake, err := client.Handshake(ctx)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("%w: %v", workerruntime.ErrProtocol, err)
	}
	if handshake.ProtocolVersion != workeripc.ProtocolVersion || handshake.WorkerVersion != workeripc.WorkerVersion {
		_ = client.Close()
		return nil, workerruntime.ErrProtocol
	}
	return &session{client: client}, nil
}

type session struct{ client *workeripc.Session }

func (s *session) StartRun(ctx context.Context, policy workerruntime.RunPolicy) error {
	capabilities := make([]workeripc.ProcessCapabilityDTO, 0, len(policy.Capabilities))
	for _, capability := range policy.Capabilities {
		capabilities = append(capabilities, workeripc.ProcessCapabilityDTO{ID: capability.ID, Executable: capability.Executable, ArgumentPrefix: append([]string(nil), capability.Arguments...), MaxArguments: len(capability.Arguments), EnvironmentNames: append([]string(nil), capability.EnvironmentNames...), MaxTimeoutMS: capability.Timeout.Milliseconds(), MaxOutputBytes: capability.MaxOutputBytes})
	}
	if err := s.client.StartRun(ctx, workeripc.StartRunPayload{RunID: policy.RunID, WorktreeRoot: policy.WorktreeRoot, Capabilities: capabilities}); err != nil {
		return mapError(err)
	}
	return nil
}

func (s *session) RunTool(ctx context.Context, request workerruntime.ToolRequest) (workerruntime.ToolResult, error) {
	type outcome struct {
		result workeripc.ToolResultPayload
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		deadline := responseDeadline(request.Timeout)
		callCtx, cancel := context.WithTimeout(context.Background(), deadline)
		defer cancel()
		result, err := s.client.ExecuteTool(callCtx, workeripc.ExecuteToolPayload{RunID: request.RunID, ToolCallID: request.CallID, CapabilityID: request.CapabilityID, Arguments: append([]string(nil), request.Arguments...), WorkingDirectory: request.WorkingDirectory, Environment: copyEnvironment(request.Environment), TimeoutMS: request.Timeout.Milliseconds(), MaxOutputBytes: request.MaxOutputBytes})
		if errors.Is(err, context.DeadlineExceeded) {
			_ = s.client.Close()
			err = fmt.Errorf("%w: tool response deadline exceeded", workeripc.ErrUnexpectedMessage)
		}
		done <- outcome{result: result, err: err}
	}()
	select {
	case completed := <-done:
		return mapResult(completed.result, completed.err)
	case <-ctx.Done():
		cancelCtx, cancel := context.WithTimeout(context.Background(), cancelTimeout)
		cancelErr := s.client.CancelTool(cancelCtx, workeripc.CancelToolPayload{RunID: request.RunID, ToolCallID: request.CallID})
		cancel()
		if cancelErr != nil {
			_ = s.client.Close()
			return workerruntime.ToolResult{}, fmt.Errorf("%w: cancellation outcome is unknown", workerruntime.ErrProtocol)
		}
		select {
		case completed := <-done:
			return mapResult(completed.result, completed.err)
		case <-time.After(cancelTimeout):
			_ = s.client.Close()
			return workerruntime.ToolResult{}, fmt.Errorf("%w: cancellation result timed out", workerruntime.ErrProtocol)
		}
	}
}

func responseDeadline(toolTimeout time.Duration) time.Duration {
	if toolTimeout <= 0 {
		toolTimeout = 10 * time.Minute
	}
	return toolTimeout + cancelTimeout
}

func (s *session) Shutdown(ctx context.Context) error { return mapError(s.client.Shutdown(ctx)) }
func (s *session) Close() error                       { return s.client.Close() }

func mapResult(result workeripc.ToolResultPayload, err error) (workerruntime.ToolResult, error) {
	if err != nil {
		return workerruntime.ToolResult{}, mapError(err)
	}
	state, valid := mapState(result.State)
	if !valid {
		return workerruntime.ToolResult{}, fmt.Errorf("%w: unknown tool state", workerruntime.ErrProtocol)
	}
	mapped := workerruntime.ToolResult{State: state, ExitCode: result.ExitCode, StdoutBytes: result.StdoutBytes, StderrBytes: result.StderrBytes, StdoutSHA256: result.StdoutSHA256, StderrSHA256: result.StderrSHA256}
	if result.StartedAt != "" {
		parsed, parseErr := time.Parse(time.RFC3339Nano, result.StartedAt)
		if parseErr != nil {
			return workerruntime.ToolResult{}, fmt.Errorf("%w: invalid tool start time", workerruntime.ErrProtocol)
		}
		mapped.StartedAt = parsed
	}
	if result.FinishedAt != "" {
		parsed, parseErr := time.Parse(time.RFC3339Nano, result.FinishedAt)
		if parseErr != nil {
			return workerruntime.ToolResult{}, fmt.Errorf("%w: invalid tool finish time", workerruntime.ErrProtocol)
		}
		mapped.FinishedAt = parsed
	}
	if mapped.State != workerruntime.ToolSucceeded || mapped.ExitCode != 0 {
		return mapped, workerruntime.ErrExecutionFailed
	}
	return mapped, nil
}

func mapState(state string) (workerruntime.ToolState, bool) {
	switch state {
	case string(workerruntime.ToolSucceeded):
		return workerruntime.ToolSucceeded, true
	case string(workerruntime.ToolFailed):
		return workerruntime.ToolFailed, true
	case string(workerruntime.ToolCanceled):
		return workerruntime.ToolCanceled, true
	case string(workerruntime.ToolTimedOut):
		return workerruntime.ToolTimedOut, true
	case string(workerruntime.ToolOutputLimited):
		return workerruntime.ToolOutputLimited, true
	default:
		return "", false
	}
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	var workerError *workeripc.WorkerError
	if errors.As(err, &workerError) {
		return fmt.Errorf("%w: %s", workerruntime.ErrExecutionFailed, workerError.Code)
	}
	if errors.Is(err, workeripc.ErrClientClosed) || errors.Is(err, workeripc.ErrVersionMismatch) || errors.Is(err, workeripc.ErrUnexpectedMessage) {
		return fmt.Errorf("%w: %v", workerruntime.ErrProtocol, err)
	}
	return fmt.Errorf("%w: %v", workerruntime.ErrUnavailable, err)
}

func copyEnvironment(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
