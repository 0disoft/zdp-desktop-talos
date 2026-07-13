package bootstrap

import (
	"path/filepath"
	"testing"

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
