package workeripc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

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
