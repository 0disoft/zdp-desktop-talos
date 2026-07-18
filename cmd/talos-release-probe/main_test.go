package main

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAuthorizedTemporaryPathRequiresOwnedRunnerTempFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("RUNNER_TEMP", root)
	inside := filepath.Join(root, "talos", "backup.talos-backup")
	resolved, err := authorizedTemporaryPath(inside)
	if err != nil || resolved != filepath.Clean(inside) {
		t.Fatalf("authorizedTemporaryPath() = %q, %v", resolved, err)
	}
	for _, unsafe := range []string{root, filepath.Join(root, "..", "escape.talos-backup")} {
		if _, err := authorizedTemporaryPath(unsafe); err == nil {
			t.Errorf("authorizedTemporaryPath(%q) unexpectedly succeeded", unsafe)
		}
	}
}

func TestRunFailsClosedWithoutExplicitWindowsCIEnablement(t *testing.T) {
	t.Setenv("CI", "false")
	t.Setenv("TALOS_RELEASE_PROBE", "0")
	if _, err := run(context.Background(), []string{"create", "--retention-days", "30"}); err == nil {
		t.Fatal("run() unexpectedly succeeded outside enabled CI")
	}
	if runtime.GOOS != "windows" {
		t.Log("non-Windows hosts always reject the release probe")
	}
}

func TestRunRejectsInvalidCommandBeforeOpeningVaultRuntime(t *testing.T) {
	t.Setenv("CI", "false")
	t.Setenv("TALOS_RELEASE_PROBE", "0")
	_, err := run(context.Background(), []string{"unknown"})
	if err == nil {
		t.Fatal("run() unexpectedly accepted an unknown command")
	}
	if err.Error() != usageError().Error() {
		t.Fatalf("run() error = %q, want usage error before environment authorization", err)
	}
}

func TestParseCommandRejectsInvalidArgumentsWithoutRuntimeState(t *testing.T) {
	t.Setenv("RUNNER_TEMP", t.TempDir())
	for name, arguments := range map[string][]string{
		"missing create retention":  {"create"},
		"missing inspect identity":  {"inspect", "--expected-revision", "1", "--expected-retention-days", "30"},
		"unsafe backup destination": {"backup", "--vault-id", "vault", "--expected-revision", "1", "--destination", filepath.Join(t.TempDir(), "backup.talos-backup")},
		"unexpected trailing value": {"purge", "--vault-id", "vault", "--expected-revision", "1", "--confirmation", "vault", "extra"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseCommand(arguments); err == nil {
				t.Fatalf("parseCommand(%q) unexpectedly succeeded", arguments)
			}
		})
	}
}
