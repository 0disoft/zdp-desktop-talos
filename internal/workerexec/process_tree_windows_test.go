//go:build windows

package workerexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkerExecTreeHelper(t *testing.T) {
	mode := os.Getenv("TALOS_TREE_MODE")
	if mode == "" {
		return
	}
	ready, sentinel := os.Getenv("TALOS_READY_PATH"), os.Getenv("TALOS_SENTINEL_PATH")
	if mode == "parent" {
		executable, _ := os.Executable()
		command := exec.Command(executable, "-test.run=TestWorkerExecTreeHelper")
		command.Env = append(os.Environ(), "TALOS_TREE_MODE=child")
		if err := command.Start(); err != nil {
			os.Exit(21)
		}
		if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil {
			os.Exit(22)
		}
		time.Sleep(10 * time.Second)
		return
	}
	time.Sleep(800 * time.Millisecond)
	_ = os.WriteFile(sentinel, []byte("survived"), 0o600)
}

func TestExecutorTimeoutKillsWindowsChildTree(t *testing.T) {
	executable, root := workerTestInputs(t)
	ready, sentinel := filepath.Join(root, "ready"), filepath.Join(root, "sentinel")
	executor, err := New(Policy{WorktreeRoot: root, Capabilities: []Capability{{ID: "tree", Executable: executable, ArgumentPrefix: []string{"-test.run=TestWorkerExecTreeHelper"}, EnvironmentNames: []string{"TALOS_TREE_MODE", "TALOS_READY_PATH", "TALOS_SENTINEL_PATH"}, MaxTimeout: 2 * time.Second}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), Request{CapabilityID: "tree", Arguments: []string{"-test.run=TestWorkerExecTreeHelper"}, Environment: map[string]string{"TALOS_TREE_MODE": "parent", "TALOS_READY_PATH": ready, "TALOS_SENTINEL_PATH": sentinel}, Timeout: 300 * time.Millisecond})
	if !errors.Is(err, ErrTimedOut) {
		t.Fatalf("timeout error=%v", err)
	}
	if _, err := os.Stat(ready); err != nil {
		t.Fatalf("child was not started: %v", err)
	}
	time.Sleep(time.Second)
	if _, err := os.Stat(sentinel); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("descendant survived job termination: %v", err)
	}
}
