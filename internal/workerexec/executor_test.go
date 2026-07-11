package workerexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkerExecHelper(t *testing.T) {
	mode := os.Getenv("TALOS_HELPER_MODE")
	if mode == "" {
		return
	}
	switch mode {
	case "echo":
		_, _ = fmt.Fprintf(os.Stdout, "allowed=%s secret=%s", os.Getenv("TALOS_ALLOWED"), os.Getenv("TALOS_PARENT_SECRET"))
		_, _ = fmt.Fprint(os.Stderr, "stderr-marker")
	case "output":
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", 8192))
	case "combined-output":
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", 800))
		_, _ = fmt.Fprint(os.Stderr, strings.Repeat("y", 800))
	case "sleep":
		time.Sleep(10 * time.Second)
	}
}

func TestExecutorUsesArgvScopedEnvironmentAndWorktree(t *testing.T) {
	executable, root := workerTestInputs(t)
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	executor, err := New(Policy{WorktreeRoot: root, Capabilities: []Capability{{ID: "test", Executable: executable, ArgumentPrefix: []string{"-test.run=TestWorkerExecHelper"}, EnvironmentNames: []string{"TALOS_HELPER_MODE", "TALOS_ALLOWED"}}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TALOS_PARENT_SECRET", "must-not-leak")
	result, err := executor.Execute(context.Background(), Request{CapabilityID: "test", Arguments: []string{"-test.run=TestWorkerExecHelper"}, WorkingDirectory: "child", Environment: map[string]string{"TALOS_HELPER_MODE": "echo", "TALOS_ALLOWED": "yes"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || !strings.HasPrefix(string(result.Stdout), "allowed=yes secret=") || strings.Contains(string(result.Stdout), "must-not-leak") || string(result.Stderr) != "stderr-marker" {
		t.Fatalf("result=%+v", result)
	}
	if result.StartedAt.IsZero() || result.FinishedAt.Before(result.StartedAt) {
		t.Fatalf("times=%+v", result)
	}
}

func TestExecutorRejectsScopeEnvironmentAndShellExpansion(t *testing.T) {
	t.Parallel()
	executable, root := workerTestInputs(t)
	executor, err := New(Policy{WorktreeRoot: root, Capabilities: []Capability{{ID: "test", Executable: executable, ArgumentPrefix: []string{"-test.run=TestWorkerExecHelper"}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name     string
		request  Request
		expected error
	}{
		{"capability", Request{CapabilityID: "other"}, ErrCapabilityDenied},
		{"argv-prefix", Request{CapabilityID: "test", Arguments: []string{"-test.run=Other"}}, ErrCapabilityDenied},
		{"path", Request{CapabilityID: "test", Arguments: []string{"-test.run=TestWorkerExecHelper"}, WorkingDirectory: "../outside"}, ErrPathEscape},
		{"environment", Request{CapabilityID: "test", Arguments: []string{"-test.run=TestWorkerExecHelper"}, Environment: map[string]string{"SECRET": "value"}}, ErrEnvironmentDenied},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := executor.Execute(context.Background(), testCase.request); !errors.Is(err, testCase.expected) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	shell := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	if _, err := os.Stat(shell); err == nil {
		if _, err := New(Policy{WorktreeRoot: root, Capabilities: []Capability{{ID: "shell", Executable: shell}}}); !errors.Is(err, ErrInvalidPolicy) {
			t.Fatalf("shell policy error=%v", err)
		}
	}
}

func TestExecutorEnforcesOutputLimitAndTimeout(t *testing.T) {
	t.Parallel()
	executable, root := workerTestInputs(t)
	executor, err := New(Policy{WorktreeRoot: root, Capabilities: []Capability{{ID: "test", Executable: executable, ArgumentPrefix: []string{"-test.run=TestWorkerExecHelper"}, EnvironmentNames: []string{"TALOS_HELPER_MODE"}, MaxTimeout: 3 * time.Second, MaxOutputBytes: 1024}}})
	if err != nil {
		t.Fatal(err)
	}
	output, err := executor.Execute(context.Background(), Request{CapabilityID: "test", Arguments: []string{"-test.run=TestWorkerExecHelper"}, Environment: map[string]string{"TALOS_HELPER_MODE": "output"}})
	if !errors.Is(err, ErrOutputLimit) || len(output.Stdout) != 1024 {
		t.Fatalf("output=%d err=%v", len(output.Stdout), err)
	}
	combined, err := executor.Execute(context.Background(), Request{CapabilityID: "test", Arguments: []string{"-test.run=TestWorkerExecHelper"}, Environment: map[string]string{"TALOS_HELPER_MODE": "combined-output"}})
	if !errors.Is(err, ErrOutputLimit) || len(combined.Stdout)+len(combined.Stderr) != 1024 {
		t.Fatalf("combined=%d err=%v", len(combined.Stdout)+len(combined.Stderr), err)
	}
	started := time.Now()
	_, err = executor.Execute(context.Background(), Request{CapabilityID: "test", Arguments: []string{"-test.run=TestWorkerExecHelper"}, Environment: map[string]string{"TALOS_HELPER_MODE": "sleep"}, Timeout: 150 * time.Millisecond})
	if !errors.Is(err, ErrTimedOut) {
		t.Fatalf("timeout error=%v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("timeout elapsed=%s", elapsed)
	}
}

func workerTestInputs(t *testing.T) (string, string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	return executable, t.TempDir()
}
