package bootstrap

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/application/executionruntime"
)

func TestDefaultExecutionFactoryBuildsTrustedGoPolicy(t *testing.T) {
	t.Parallel()
	factory, err := NewDefaultExecutionFactory(t.TempDir(), filepath.Join(t.TempDir(), "talos-worker.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := factory.New(nil); err != executionruntime.ErrInvalidRequest {
		t.Fatalf("nil store error=%v", err)
	}
}

func TestGoVerificationEnvironmentUsesOwnedOfflineDirectories(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	environment, err := goVerificationEnvironment(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"HOME", "USERPROFILE", "GOCACHE", "GOMODCACHE", "GOPATH"} {
		path := environment[name]
		info, statErr := os.Stat(path)
		if statErr != nil || !info.IsDir() || !filepath.IsAbs(path) {
			t.Fatalf("%s=%q info=%v error=%v", name, path, info, statErr)
		}
		if relative, relErr := filepath.Rel(root, path); relErr != nil || relative == ".." || filepath.IsAbs(relative) {
			t.Fatalf("%s escaped root: %q", name, path)
		}
	}
	for name, expected := range map[string]string{"GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off", "GOENV": "off", "GOFLAGS": "-mod=readonly"} {
		if environment[name] != expected {
			t.Fatalf("%s=%q", name, environment[name])
		}
	}
}

func TestGoVerificationEnvironmentRunsDependencyFreeTests(t *testing.T) {
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		t.Skip("Go executable unavailable")
	}
	root := t.TempDir()
	environment, err := goVerificationEnvironment(filepath.Join(root, "talos-data"))
	if err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(root, "module")
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte("module example.invalid/offline\n\ngo 1.26.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "offline_test.go"), []byte("package offline\n\nimport \"testing\"\n\nfunc TestOffline(t *testing.T) {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP"} {
		if value := os.Getenv(name); value != "" {
			environment[name] = value
		}
	}
	names := make([]string, 0, len(environment))
	for name := range environment {
		names = append(names, name)
	}
	sort.Strings(names)
	fresh := make([]string, 0, len(names))
	for _, name := range names {
		fresh = append(fresh, name+"="+environment[name])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, goExecutable, "test", "./...")
	command.Dir = module
	command.Env = fresh
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("dependency-free go test failed: %v\n%s\nenvironment names=%s", err, output, strings.Join(names, ","))
	}
}

func TestSiblingWorkerExecutableIsAbsoluteAndAdjacent(t *testing.T) {
	t.Parallel()
	worker, err := SiblingWorkerExecutable()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(worker) || filepath.Dir(worker) == worker {
		t.Fatalf("worker=%q", worker)
	}
}
