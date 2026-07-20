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

	"golang.org/x/sys/windows"
)

func TestWorkerExecTreeHelper(t *testing.T) {
	mode := os.Getenv("TALOS_TREE_MODE")
	if mode == "" {
		return
	}
	ready, sentinel := os.Getenv("TALOS_READY_PATH"), os.Getenv("TALOS_SENTINEL_PATH")
	if mode == "activation-probe" {
		if err := os.WriteFile(ready, []byte("activated"), 0o600); err != nil {
			os.Exit(23)
		}
		return
	}
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

func TestWindowsProcessStartsSuspendedUntilJobActivation(t *testing.T) {
	executable, root := workerTestInputs(t)
	marker := filepath.Join(root, "activated")
	command := exec.Command(executable, "-test.run=TestWorkerExecTreeHelper")
	command.Env = append(os.Environ(), "TALOS_TREE_MODE=activation-probe", "TALOS_READY_PATH="+marker)
	group, err := newProcessGroup()
	if err != nil {
		t.Fatal(err)
	}
	defer group.close()
	if err := group.prepare(command); err != nil {
		t.Fatal(err)
	}
	if command.SysProcAttr == nil || command.SysProcAttr.CreationFlags&windows.CREATE_SUSPENDED == 0 {
		t.Fatal("Windows process was not configured for suspended creation")
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = group.terminate()
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	}()
	time.Sleep(250 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("process executed before job activation: %v", err)
	}
	if err := group.activate(command.Process); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	if payload, err := os.ReadFile(marker); err != nil || string(payload) != "activated" {
		t.Fatalf("activation marker=%q error=%v", payload, err)
	}
}

func TestExecutorCancellationKillsWindowsChildTree(t *testing.T) {
	executable, root := workerTestInputs(t)
	ready, sentinel := filepath.Join(root, "ready"), filepath.Join(root, "sentinel")
	executor, err := New(Policy{WorktreeRoot: root, Capabilities: []Capability{{ID: "tree", Executable: executable, ArgumentPrefix: []string{"-test.run=TestWorkerExecTreeHelper"}, EnvironmentNames: []string{"TALOS_TREE_MODE", "TALOS_READY_PATH", "TALOS_SENTINEL_PATH"}, MaxTimeout: 5 * time.Second}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, executeErr := executor.Execute(ctx, Request{CapabilityID: "tree", Arguments: []string{"-test.run=TestWorkerExecTreeHelper"}, Environment: map[string]string{"TALOS_TREE_MODE": "parent", "TALOS_READY_PATH": ready, "TALOS_SENTINEL_PATH": sentinel}, Timeout: 5 * time.Second})
		done <- executeErr
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, statErr := os.Stat(ready); statErr == nil {
			break
		} else if !errors.Is(statErr, os.ErrNotExist) {
			t.Fatal(statErr)
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("child process did not report readiness")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err = <-done:
		if !errors.Is(err, ErrCanceled) {
			t.Fatalf("cancellation error=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled process tree was not reaped")
	}
	time.Sleep(time.Second)
	if _, err := os.Stat(sentinel); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("descendant survived job termination: %v", err)
	}
}
