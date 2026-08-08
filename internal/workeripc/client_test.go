package workeripc

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionPerformsTypedHandshakeRunAndToolLifecycle(t *testing.T) {
	session := newPipeSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	handshake, err := session.Handshake(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if handshake.ProtocolVersion != ProtocolVersion || handshake.WorkerVersion != WorkerVersion {
		t.Fatalf("handshake=%+v", handshake)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := session.StartRun(ctx, StartRunPayload{RunID: "run-client", WorktreeRoot: root, Capabilities: []ProcessCapabilityDTO{{ID: "test", Executable: executable, ArgumentPrefix: []string{"-test.run=TestIPCExecHelper"}, MaxArguments: 1, EnvironmentNames: []string{"TALOS_IPC_HELPER_MODE"}, MaxTimeoutMS: 2000, MaxOutputBytes: 2048}}}); err != nil {
		t.Fatal(err)
	}
	result, err := session.ExecuteTool(ctx, ExecuteToolPayload{RunID: "run-client", ToolCallID: "call-client", CapabilityID: "test", Arguments: []string{"-test.run=TestIPCExecHelper"}, Environment: map[string]string{"TALOS_IPC_HELPER_MODE": "echo"}, TimeoutMS: 1000, MaxOutputBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "succeeded" || result.ExitCode != 0 || result.StdoutBytes < len("ipc-echo") || len(result.StdoutSHA256) != 64 {
		t.Fatalf("result=%+v", result)
	}
	if err := session.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Handshake(ctx); !errors.Is(err, ErrClientClosed) {
		t.Fatalf("closed handshake error=%v", err)
	}
}

func TestSessionMapsWorkerErrorsWithoutLeakingProtocolDetails(t *testing.T) {
	session := newPipeSession(t)
	defer session.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := session.Handshake(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := session.ExecuteTool(ctx, ExecuteToolPayload{RunID: "missing", ToolCallID: "call", CapabilityID: "test"})
	var workerError *WorkerError
	if !errors.As(err, &workerError) || workerError.Code != "WORKER_RUN_NOT_STARTED" || workerError.Error() != "worker request failed: WORKER_RUN_NOT_STARTED" {
		t.Fatalf("worker error=%#v", err)
	}
}

func TestStartSessionRejectsRelativeExecutable(t *testing.T) {
	if _, err := StartSession("talos-worker"); err == nil {
		t.Fatal("relative worker executable was accepted")
	}
}

func newPipeSession(t *testing.T) *Session {
	t.Helper()
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- Serve(inputReader, outputWriter)
		_ = outputWriter.Close()
		_ = inputReader.Close()
	}()
	wait := func() error { return <-serverDone }
	kill := func() error {
		_ = inputWriter.Close()
		_ = outputReader.Close()
		return nil
	}
	session, err := NewSession(inputWriter, outputReader, wait, kill)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = session.Close()
	})
	return session
}
