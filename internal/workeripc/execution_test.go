package workeripc

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIPCExecHelper(t *testing.T) {
	switch os.Getenv("TALOS_IPC_HELPER_MODE") {
	case "echo":
		_, _ = fmt.Fprint(os.Stdout, "ipc-echo")
	case "large-output":
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", 3*1024*1024))
	case "sleep":
		time.Sleep(10 * time.Second)
	}
}

func TestServerRequiresRunPolicyAndExecutesThenCancelsTools(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	serverDone := make(chan error, 1)
	go func() { serverDone <- Serve(inputReader, outputWriter) }()
	encoder, decoder := NewEncoder(inputWriter, MaxFrameSize), NewDecoder(outputReader, MaxFrameSize)

	writePayload(t, encoder, "execute_tool", "before-run", ExecuteToolPayload{RunID: "run-1", ToolCallID: "tool-before", CapabilityID: "test", Arguments: []string{"-test.run=TestIPCExecHelper"}})
	response := readMessage(t, decoder)
	if response.Error == nil || response.Error.Code != "WORKER_RUN_NOT_STARTED" {
		t.Fatalf("before run=%+v", response)
	}

	writePayload(t, encoder, "start_run", "start", StartRunPayload{RunID: "run-1", WorktreeRoot: t.TempDir(), Capabilities: []ProcessCapabilityDTO{{ID: "test", Executable: executable, ArgumentPrefix: []string{"-test.run=TestIPCExecHelper"}, EnvironmentNames: []string{"TALOS_IPC_HELPER_MODE"}, MaxTimeoutMS: 3000, MaxOutputBytes: 4 * 1024 * 1024}}})
	if response = readMessage(t, decoder); response.Type != "start_run_result" || response.Error != nil {
		t.Fatalf("start=%+v", response)
	}

	writePayload(t, encoder, "execute_tool", "echo-request", ExecuteToolPayload{RunID: "run-1", ToolCallID: "tool-echo", CapabilityID: "test", Arguments: []string{"-test.run=TestIPCExecHelper"}, Environment: map[string]string{"TALOS_IPC_HELPER_MODE": "echo"}, TimeoutMS: 2000, MaxOutputBytes: 2048})
	response = readMessage(t, decoder)
	if response.Type != "tool_result" {
		t.Fatalf("echo response=%+v", response)
	}
	var echo ToolResultPayload
	if err := json.Unmarshal(response.Payload, &echo); err != nil {
		t.Fatal(err)
	}
	if echo.State != "succeeded" || echo.ExitCode != 0 || echo.StdoutBytes < len("ipc-echo") || len(echo.StdoutSHA256) != 64 {
		t.Fatalf("echo=%+v", echo)
	}

	writePayload(t, encoder, "execute_tool", "large-request", ExecuteToolPayload{RunID: "run-1", ToolCallID: "tool-large", CapabilityID: "test", Arguments: []string{"-test.run=TestIPCExecHelper"}, Environment: map[string]string{"TALOS_IPC_HELPER_MODE": "large-output"}, TimeoutMS: 2000, MaxOutputBytes: 4 * 1024 * 1024})
	response = readMessage(t, decoder)
	var large ToolResultPayload
	if err := json.Unmarshal(response.Payload, &large); err != nil {
		t.Fatal(err)
	}
	if large.State != "succeeded" || large.StdoutBytes < 3*1024*1024 || len(large.StdoutSHA256) != 64 || len(response.Payload) > 2048 {
		t.Fatalf("large result=%+v payload_bytes=%d", large, len(response.Payload))
	}

	writePayload(t, encoder, "execute_tool", "sleep-request", ExecuteToolPayload{RunID: "run-1", ToolCallID: "tool-sleep", CapabilityID: "test", Arguments: []string{"-test.run=TestIPCExecHelper"}, Environment: map[string]string{"TALOS_IPC_HELPER_MODE": "sleep"}, TimeoutMS: 3000})
	writePayload(t, encoder, "execute_tool", "duplicate-request", ExecuteToolPayload{RunID: "run-1", ToolCallID: "tool-sleep", CapabilityID: "test", Arguments: []string{"-test.run=TestIPCExecHelper"}})
	response = readMessage(t, decoder)
	if response.Error == nil || response.Error.Code != "WORKER_TOOL_ALREADY_ACTIVE" {
		t.Fatalf("duplicate=%+v", response)
	}
	writePayload(t, encoder, "execute_tool", "busy-request", ExecuteToolPayload{RunID: "run-1", ToolCallID: "tool-other", CapabilityID: "test", Arguments: []string{"-test.run=TestIPCExecHelper"}})
	response = readMessage(t, decoder)
	if response.Error == nil || response.Error.Code != "WORKER_BUSY" || !response.Error.Retryable {
		t.Fatalf("busy=%+v", response)
	}
	writePayload(t, encoder, "cancel_tool", "cancel-request", CancelToolPayload{RunID: "run-1", ToolCallID: "tool-sleep"})
	first, second := readMessage(t, decoder), readMessage(t, decoder)
	responses := map[string]Message{first.Type: first, second.Type: second}
	if responses["cancel_tool_result"].Type == "" || responses["tool_result"].Type == "" {
		t.Fatalf("cancel responses=%+v %+v", first, second)
	}
	var canceled ToolResultPayload
	if err := json.Unmarshal(responses["tool_result"].Payload, &canceled); err != nil {
		t.Fatal(err)
	}
	if canceled.State != "canceled" {
		t.Fatalf("canceled=%+v", canceled)
	}

	if err := encoder.Write(Message{Version: ProtocolVersion, Type: "shutdown", RequestID: "shutdown"}); err != nil {
		t.Fatal(err)
	}
	if response = readMessage(t, decoder); response.Type != "shutdown_result" {
		t.Fatalf("shutdown=%+v", response)
	}
	_ = inputWriter.Close()
	_ = outputReader.Close()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}
}

func TestServerClosesTransportWhenAsyncResultWriteFails(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	serverDone := make(chan error, 1)
	go func() { serverDone <- Serve(inputReader, outputWriter) }()
	encoder, decoder := NewEncoder(inputWriter, MaxFrameSize), NewDecoder(outputReader, MaxFrameSize)
	writePayload(t, encoder, "start_run", "start", StartRunPayload{RunID: "run-fail", WorktreeRoot: t.TempDir(), Capabilities: []ProcessCapabilityDTO{{ID: "test", Executable: executable, ArgumentPrefix: []string{"-test.run=TestIPCExecHelper"}, EnvironmentNames: []string{"TALOS_IPC_HELPER_MODE"}, MaxTimeoutMS: 2000, MaxOutputBytes: 2048}}})
	if response := readMessage(t, decoder); response.Type != "start_run_result" {
		t.Fatalf("start=%+v", response)
	}
	writePayload(t, encoder, "execute_tool", "execute", ExecuteToolPayload{RunID: "run-fail", ToolCallID: "tool", CapabilityID: "test", Arguments: []string{"-test.run=TestIPCExecHelper"}, Environment: map[string]string{"TALOS_IPC_HELPER_MODE": "echo"}, TimeoutMS: 1000, MaxOutputBytes: 1024})
	_ = outputReader.Close()
	select {
	case <-serverDone:
	case <-time.After(5 * time.Second):
		t.Fatal("server kept the broken transport open")
	}
	_ = inputWriter.Close()
}

func writePayload(t *testing.T, encoder *Encoder, messageType, requestID string, payload any) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := encoder.Write(Message{Version: ProtocolVersion, Type: messageType, RequestID: requestID, Payload: encoded}); err != nil {
		t.Fatal(err)
	}
}

func readMessage(t *testing.T, decoder *Decoder) Message {
	t.Helper()
	type result struct {
		message Message
		err     error
	}
	done := make(chan result, 1)
	go func() { message, err := decoder.Read(); done <- result{message, err} }()
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		return result.message
	case <-time.After(5 * time.Second):
		t.Fatal("worker response timed out")
		return Message{}
	}
}
