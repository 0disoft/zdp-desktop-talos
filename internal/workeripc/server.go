package workeripc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/workerexec"
)

const WorkerVersion = "0.8.0"

type StartRunPayload struct {
	RunID        string                 `json:"run_id"`
	WorktreeRoot string                 `json:"worktree_root"`
	Capabilities []ProcessCapabilityDTO `json:"capabilities"`
}

type ProcessCapabilityDTO struct {
	ID               string   `json:"id"`
	Executable       string   `json:"executable"`
	ArgumentPrefix   []string `json:"argument_prefix"`
	MaxArguments     int      `json:"max_arguments"`
	EnvironmentNames []string `json:"environment_names"`
	MaxTimeoutMS     int64    `json:"max_timeout_ms"`
	MaxOutputBytes   int      `json:"max_output_bytes"`
}

type ExecuteToolPayload struct {
	RunID            string            `json:"run_id"`
	ToolCallID       string            `json:"tool_call_id"`
	CapabilityID     string            `json:"capability_id"`
	Arguments        []string          `json:"arguments"`
	WorkingDirectory string            `json:"working_directory"`
	Environment      map[string]string `json:"environment"`
	TimeoutMS        int64             `json:"timeout_ms"`
	MaxOutputBytes   int               `json:"max_output_bytes"`
}

type CancelToolPayload struct {
	RunID      string `json:"run_id"`
	ToolCallID string `json:"tool_call_id"`
}

type ToolResultPayload struct {
	RunID      string `json:"run_id"`
	ToolCallID string `json:"tool_call_id"`
	State      string `json:"state"`
	ExitCode   int    `json:"exit_code"`
	Stdout     []byte `json:"stdout"`
	Stderr     []byte `json:"stderr"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
}

type executor interface {
	Execute(context.Context, workerexec.Request) (workerexec.Result, error)
}

type server struct {
	encoder     *Encoder
	writeMu     sync.Mutex
	stateMu     sync.Mutex
	runID       string
	executor    executor
	active      map[string]context.CancelFunc
	wait        sync.WaitGroup
	newExecutor func(workerexec.Policy) (*workerexec.Executor, error)
}

func Serve(reader io.Reader, writer io.Writer) error {
	s := &server{encoder: NewEncoder(writer, MaxFrameSize), active: make(map[string]context.CancelFunc), newExecutor: workerexec.New}
	decoder := NewDecoder(reader, MaxFrameSize)
	for {
		message, err := decoder.Read()
		if errors.Is(err, io.EOF) {
			s.cancelAndWait()
			return nil
		}
		if err != nil {
			s.cancelAndWait()
			return err
		}
		if message.Version != ProtocolVersion {
			_ = s.write(Message{Version: ProtocolVersion, Type: "error", RequestID: message.RequestID, Error: &ErrorPayload{Code: "WORKER_PROTOCOL_VERSION_MISMATCH", Message: "worker and client protocol versions differ", Retryable: false}})
			s.cancelAndWait()
			return ErrVersionMismatch
		}
		switch message.Type {
		case "handshake":
			payload, _ := json.Marshal(HandshakePayload{ProtocolVersion: ProtocolVersion, WorkerVersion: WorkerVersion, Capabilities: []string{"heartbeat", "start_run", "execute_tool", "cancel_tool", "shutdown"}})
			if err := s.write(Message{Version: ProtocolVersion, Type: "handshake_result", RequestID: message.RequestID, Payload: payload}); err != nil {
				return err
			}
		case "heartbeat":
			if err := s.write(Message{Version: ProtocolVersion, Type: "heartbeat_result", RequestID: message.RequestID, Payload: json.RawMessage(`{"status":"ok"}`)}); err != nil {
				return err
			}
		case "start_run":
			if err := s.startRun(message); err != nil {
				return err
			}
		case "execute_tool":
			if err := s.executeTool(message); err != nil {
				return err
			}
		case "cancel_tool":
			if err := s.cancelTool(message); err != nil {
				return err
			}
		case "shutdown":
			s.cancelAndWait()
			if err := s.write(Message{Version: ProtocolVersion, Type: "shutdown_result", RequestID: message.RequestID, Payload: json.RawMessage(`{"status":"stopped"}`)}); err != nil {
				return err
			}
			return nil
		default:
			if err := s.writeError(message.RequestID, "WORKER_MESSAGE_UNSUPPORTED", fmt.Sprintf("unsupported message type %q", message.Type), false); err != nil {
				return err
			}
		}
	}
}

func (s *server) startRun(message Message) error {
	var payload StartRunPayload
	if err := decodePayload(message.Payload, &payload); err != nil || payload.RunID == "" || len(payload.RunID) > 128 || len(payload.Capabilities) == 0 || len(payload.Capabilities) > 64 {
		return s.writeError(message.RequestID, "WORKER_START_RUN_INVALID", "run policy is invalid", false)
	}
	capabilities := make([]workerexec.Capability, 0, len(payload.Capabilities))
	for _, item := range payload.Capabilities {
		if item.MaxTimeoutMS < 0 || item.MaxTimeoutMS > int64(workerexec.AbsoluteMaxTimeout/time.Millisecond) {
			return s.writeError(message.RequestID, "WORKER_START_RUN_INVALID", "run policy is invalid", false)
		}
		capabilities = append(capabilities, workerexec.Capability{ID: item.ID, Executable: item.Executable, ArgumentPrefix: item.ArgumentPrefix, MaxArguments: item.MaxArguments, EnvironmentNames: item.EnvironmentNames, MaxTimeout: time.Duration(item.MaxTimeoutMS) * time.Millisecond, MaxOutputBytes: item.MaxOutputBytes})
	}
	executor, err := s.newExecutor(workerexec.Policy{WorktreeRoot: payload.WorktreeRoot, Capabilities: capabilities})
	if err != nil {
		return s.writeError(message.RequestID, "WORKER_START_RUN_DENIED", "run policy was denied", false)
	}
	s.stateMu.Lock()
	if s.runID != "" {
		s.stateMu.Unlock()
		return s.writeError(message.RequestID, "WORKER_RUN_ALREADY_STARTED", "worker already owns a run", false)
	}
	s.runID, s.executor = payload.RunID, executor
	s.stateMu.Unlock()
	encoded, _ := json.Marshal(struct{ RunID, State string }{payload.RunID, "ready"})
	return s.write(Message{Version: ProtocolVersion, Type: "start_run_result", RequestID: message.RequestID, Payload: encoded})
}

func (s *server) executeTool(message Message) error {
	var payload ExecuteToolPayload
	if err := decodePayload(message.Payload, &payload); err != nil || payload.RunID == "" || payload.ToolCallID == "" || len(payload.ToolCallID) > 128 || payload.TimeoutMS < 0 || payload.TimeoutMS > int64(workerexec.AbsoluteMaxTimeout/time.Millisecond) {
		return s.writeError(message.RequestID, "WORKER_TOOL_REQUEST_INVALID", "tool request is invalid", false)
	}
	s.stateMu.Lock()
	if s.runID == "" || s.executor == nil {
		s.stateMu.Unlock()
		return s.writeError(message.RequestID, "WORKER_RUN_NOT_STARTED", "start_run is required", false)
	}
	if payload.RunID != s.runID {
		s.stateMu.Unlock()
		return s.writeError(message.RequestID, "WORKER_RUN_MISMATCH", "tool request belongs to another run", false)
	}
	if _, exists := s.active[payload.ToolCallID]; exists {
		s.stateMu.Unlock()
		return s.writeError(message.RequestID, "WORKER_TOOL_ALREADY_ACTIVE", "tool call is already active", false)
	}
	if len(s.active) > 0 {
		s.stateMu.Unlock()
		return s.writeError(message.RequestID, "WORKER_BUSY", "worker already has an active tool call", true)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.active[payload.ToolCallID] = cancel
	executor := s.executor
	s.wait.Add(1)
	s.stateMu.Unlock()
	go func() {
		defer s.wait.Done()
		result, err := executor.Execute(ctx, workerexec.Request{CapabilityID: payload.CapabilityID, Arguments: payload.Arguments, WorkingDirectory: payload.WorkingDirectory, Environment: payload.Environment, Timeout: time.Duration(payload.TimeoutMS) * time.Millisecond, MaxOutputBytes: payload.MaxOutputBytes})
		s.stateMu.Lock()
		delete(s.active, payload.ToolCallID)
		s.stateMu.Unlock()
		state := "succeeded"
		switch {
		case errors.Is(err, workerexec.ErrCanceled):
			state = "canceled"
		case errors.Is(err, workerexec.ErrTimedOut):
			state = "timed_out"
		case errors.Is(err, workerexec.ErrOutputLimit):
			state = "output_limited"
		case err != nil:
			state = "failed"
		case result.ExitCode != 0:
			state = "failed"
		}
		encoded, _ := json.Marshal(ToolResultPayload{RunID: payload.RunID, ToolCallID: payload.ToolCallID, State: state, ExitCode: result.ExitCode, Stdout: result.Stdout, Stderr: result.Stderr, StartedAt: formatTime(result.StartedAt), FinishedAt: formatTime(result.FinishedAt)})
		_ = s.write(Message{Version: ProtocolVersion, Type: "tool_result", RequestID: message.RequestID, Payload: encoded})
	}()
	return nil
}

func (s *server) cancelTool(message Message) error {
	var payload CancelToolPayload
	if err := decodePayload(message.Payload, &payload); err != nil || payload.RunID == "" || payload.ToolCallID == "" {
		return s.writeError(message.RequestID, "WORKER_CANCEL_INVALID", "cancel request is invalid", false)
	}
	s.stateMu.Lock()
	cancel, exists := s.active[payload.ToolCallID]
	runID := s.runID
	s.stateMu.Unlock()
	if payload.RunID != runID {
		return s.writeError(message.RequestID, "WORKER_RUN_MISMATCH", "cancel request belongs to another run", false)
	}
	if !exists {
		return s.writeError(message.RequestID, "WORKER_TOOL_NOT_ACTIVE", "tool call is not active", false)
	}
	cancel()
	encoded, _ := json.Marshal(struct{ RunID, ToolCallID, State string }{payload.RunID, payload.ToolCallID, "canceling"})
	return s.write(Message{Version: ProtocolVersion, Type: "cancel_tool_result", RequestID: message.RequestID, Payload: encoded})
}

func (s *server) cancelAndWait() {
	s.stateMu.Lock()
	for _, cancel := range s.active {
		cancel()
	}
	s.stateMu.Unlock()
	s.wait.Wait()
}
func (s *server) write(message Message) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.encoder.Write(message)
}
func (s *server) writeError(requestID, code, message string, retryable bool) error {
	return s.write(Message{Version: ProtocolVersion, Type: "error", RequestID: requestID, Error: &ErrorPayload{Code: code, Message: message, Retryable: retryable}})
}
func decodePayload(payload json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing payload")
	}
	return nil
}
func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
