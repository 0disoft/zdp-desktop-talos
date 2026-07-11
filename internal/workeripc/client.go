package workeripc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
)

var (
	ErrClientClosed      = errors.New("worker client is closed")
	ErrDuplicateRequest  = errors.New("worker request id is already active")
	ErrUnexpectedMessage = errors.New("worker returned an unexpected message")
)

type WorkerError struct {
	Code      string
	Message   string
	Retryable bool
}

func (e *WorkerError) Error() string { return "worker request failed: " + e.Code }

type Session struct {
	input    io.WriteCloser
	output   io.ReadCloser
	encoder  *Encoder
	decoder  *Decoder
	writeMu  sync.Mutex
	stateMu  sync.Mutex
	pending  map[string]chan response
	done     chan struct{}
	closed   bool
	terminal error
	sequence atomic.Uint64
	wait     func() error
	kill     func() error
	waitOnce sync.Once
	waitDone chan error
}

type response struct {
	message Message
	err     error
}

func NewSession(input io.WriteCloser, output io.ReadCloser, wait, kill func() error) (*Session, error) {
	if input == nil || output == nil || wait == nil || kill == nil {
		return nil, ErrClientClosed
	}
	session := &Session{input: input, output: output, encoder: NewEncoder(input, MaxFrameSize), decoder: NewDecoder(output, MaxFrameSize), pending: make(map[string]chan response), done: make(chan struct{}), wait: wait, kill: kill, waitDone: make(chan error, 1)}
	go session.readLoop()
	return session, nil
}

func StartSession(executable string) (*Session, error) {
	if executable == "" || !filepath.IsAbs(executable) {
		return nil, fmt.Errorf("worker executable is required")
	}
	canonical, err := filepath.EvalSymlinks(filepath.Clean(executable))
	if err != nil {
		return nil, fmt.Errorf("resolve worker executable: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil || info.IsDir() {
		return nil, fmt.Errorf("worker executable is unavailable")
	}
	command := exec.Command(canonical)
	command.Env = selectedWorkerEnvironment()
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open worker stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open worker stdout: %w", err)
	}
	command.Stderr = &boundedBuffer{limit: 64 * 1024}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start worker: %w", err)
	}
	return NewSession(stdin, stdout, command.Wait, func() error {
		if command.Process == nil {
			return nil
		}
		return command.Process.Kill()
	})
}

func (s *Session) Handshake(ctx context.Context) (HandshakePayload, error) {
	var result HandshakePayload
	if err := s.call(ctx, "handshake", nil, "handshake_result", &result); err != nil {
		return HandshakePayload{}, err
	}
	if result.ProtocolVersion != ProtocolVersion {
		return HandshakePayload{}, ErrVersionMismatch
	}
	return result, nil
}

func (s *Session) StartRun(ctx context.Context, payload StartRunPayload) error {
	return s.call(ctx, "start_run", payload, "start_run_result", nil)
}

func (s *Session) ExecuteTool(ctx context.Context, payload ExecuteToolPayload) (ToolResultPayload, error) {
	var result ToolResultPayload
	if err := s.call(ctx, "execute_tool", payload, "tool_result", &result); err != nil {
		return ToolResultPayload{}, err
	}
	return result, nil
}

func (s *Session) CancelTool(ctx context.Context, payload CancelToolPayload) error {
	return s.call(ctx, "cancel_tool", payload, "cancel_tool_result", nil)
}

func (s *Session) Shutdown(ctx context.Context) error {
	err := s.call(ctx, "shutdown", nil, "shutdown_result", nil)
	s.closeTransportMode(err, err != nil)
	if err != nil {
		return err
	}
	select {
	case waitErr := <-s.waitDone:
		if waitErr != nil {
			return fmt.Errorf("worker exited unsuccessfully: %w", waitErr)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Session) Close() error {
	s.closeTransport(ErrClientClosed)
	return nil
}

func (s *Session) call(ctx context.Context, messageType string, payload any, expectedType string, target any) error {
	if ctx == nil || messageType == "" || expectedType == "" {
		return ErrUnexpectedMessage
	}
	requestID := fmt.Sprintf("client-%d", s.sequence.Add(1))
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode worker request: %w", err)
	}
	result := make(chan response, 1)
	s.stateMu.Lock()
	if s.closed {
		err := s.terminal
		s.stateMu.Unlock()
		if err == nil {
			err = ErrClientClosed
		}
		return err
	}
	if _, exists := s.pending[requestID]; exists {
		s.stateMu.Unlock()
		return ErrDuplicateRequest
	}
	s.pending[requestID] = result
	s.stateMu.Unlock()
	s.writeMu.Lock()
	err = s.encoder.Write(Message{Version: ProtocolVersion, Type: messageType, RequestID: requestID, Payload: encoded})
	s.writeMu.Unlock()
	if err != nil {
		s.removePending(requestID)
		s.closeTransport(err)
		return err
	}
	select {
	case received := <-result:
		if received.err != nil {
			return received.err
		}
		if received.message.Error != nil {
			return &WorkerError{Code: received.message.Error.Code, Message: received.message.Error.Message, Retryable: received.message.Error.Retryable}
		}
		if received.message.Version != ProtocolVersion || received.message.Type != expectedType {
			s.closeTransport(ErrUnexpectedMessage)
			return ErrUnexpectedMessage
		}
		if target != nil {
			if err := decodePayload(received.message.Payload, target); err != nil {
				s.closeTransport(ErrUnexpectedMessage)
				return fmt.Errorf("decode worker response: %w", err)
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		select {
		case received := <-result:
			if received.err != nil {
				return received.err
			}
			if received.message.Error != nil {
				return &WorkerError{Code: received.message.Error.Code, Message: received.message.Error.Message, Retryable: received.message.Error.Retryable}
			}
		default:
		}
		s.stateMu.Lock()
		err := s.terminal
		s.stateMu.Unlock()
		if err == nil {
			err = ErrClientClosed
		}
		return err
	}
}

func (s *Session) readLoop() {
	for {
		message, err := s.decoder.Read()
		if err != nil {
			s.closeTransport(err)
			return
		}
		if message.Version != ProtocolVersion {
			s.closeTransport(ErrVersionMismatch)
			return
		}
		s.stateMu.Lock()
		pending, exists := s.pending[message.RequestID]
		if exists {
			delete(s.pending, message.RequestID)
		}
		s.stateMu.Unlock()
		if !exists {
			s.closeTransport(ErrUnexpectedMessage)
			return
		}
		pending <- response{message: message}
		if message.Type == "shutdown_result" {
			return
		}
	}
}

func (s *Session) removePending(requestID string) {
	s.stateMu.Lock()
	delete(s.pending, requestID)
	s.stateMu.Unlock()
}

func (s *Session) closeTransport(cause error) {
	s.closeTransportMode(cause, true)
}

func (s *Session) closeTransportMode(cause error, force bool) {
	s.stateMu.Lock()
	if s.closed {
		s.stateMu.Unlock()
		return
	}
	s.closed = true
	s.terminal = cause
	pending := s.pending
	s.pending = make(map[string]chan response)
	close(s.done)
	s.stateMu.Unlock()
	_ = s.input.Close()
	_ = s.output.Close()
	if force {
		_ = s.kill()
	}
	for _, waiter := range pending {
		waiter <- response{err: cause}
	}
	s.waitOnce.Do(func() {
		go func() { s.waitDone <- s.wait() }()
	})
}

type boundedBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - len(b.data)
	if remaining > 0 {
		if remaining > len(value) {
			remaining = len(value)
		}
		b.data = append(b.data, value[:remaining]...)
	}
	return len(value), nil
}

func selectedWorkerEnvironment() []string {
	names := []string{"SystemRoot", "WINDIR", "TEMP", "TMP", "TMPDIR"}
	result := make([]string, 0, len(names))
	for _, name := range names {
		if value, exists := os.LookupEnv(name); exists {
			result = append(result, name+"="+value)
		}
	}
	return result
}

func Probe(ctx context.Context, executable string) (HandshakePayload, error) {
	if executable == "" {
		return HandshakePayload{}, fmt.Errorf("worker executable is required")
	}
	command := exec.CommandContext(ctx, executable)
	stdin, err := command.StdinPipe()
	if err != nil {
		return HandshakePayload{}, fmt.Errorf("open worker stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return HandshakePayload{}, fmt.Errorf("open worker stdout: %w", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return HandshakePayload{}, fmt.Errorf("start worker: %w", err)
	}

	encoder := NewEncoder(stdin, MaxFrameSize)
	decoder := NewDecoder(stdout, MaxFrameSize)
	if err := encoder.Write(Message{Version: ProtocolVersion, Type: "handshake", RequestID: "doctor-handshake"}); err != nil {
		_ = command.Process.Kill()
		return HandshakePayload{}, err
	}
	response, err := decoder.Read()
	if err != nil {
		_ = command.Process.Kill()
		return HandshakePayload{}, fmt.Errorf("read worker handshake: %w", err)
	}
	if response.Error != nil {
		_ = command.Process.Kill()
		return HandshakePayload{}, fmt.Errorf("worker handshake failed: %s", response.Error.Code)
	}
	if response.Type != "handshake_result" || response.Version != ProtocolVersion {
		_ = command.Process.Kill()
		return HandshakePayload{}, ErrVersionMismatch
	}
	var handshake HandshakePayload
	if err := json.Unmarshal(response.Payload, &handshake); err != nil {
		_ = command.Process.Kill()
		return HandshakePayload{}, fmt.Errorf("decode worker handshake: %w", err)
	}
	if handshake.ProtocolVersion != ProtocolVersion {
		_ = command.Process.Kill()
		return HandshakePayload{}, ErrVersionMismatch
	}
	if err := encoder.Write(Message{Version: ProtocolVersion, Type: "shutdown", RequestID: "doctor-shutdown"}); err != nil {
		_ = command.Process.Kill()
		return HandshakePayload{}, err
	}
	if _, err := decoder.Read(); err != nil {
		_ = command.Process.Kill()
		return HandshakePayload{}, fmt.Errorf("read worker shutdown: %w", err)
	}
	if err := stdin.Close(); err != nil {
		_ = command.Process.Kill()
		return HandshakePayload{}, fmt.Errorf("close worker stdin: %w", err)
	}
	if err := command.Wait(); err != nil {
		return HandshakePayload{}, fmt.Errorf("worker exited unsuccessfully: %w", err)
	}
	return handshake, nil
}
