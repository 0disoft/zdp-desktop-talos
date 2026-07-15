package bootstrap

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/gitcli"
	"github.com/0disoft/zdp-desktop-talos/internal/adapters/workerprocess"
	"github.com/0disoft/zdp-desktop-talos/internal/application/executionruntime"
	"github.com/0disoft/zdp-desktop-talos/internal/application/patchreview"
	"github.com/0disoft/zdp-desktop-talos/internal/application/permissionbroker"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/permission"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/workerruntime"
	"github.com/0disoft/zdp-desktop-talos/internal/security/redaction"
)

const (
	verificationTimeout   = 10 * time.Minute
	verificationOutputMax = 1 << 20
)

type ExecutionFactory struct {
	broker    *permissionbroker.Broker
	worktrees *gitcli.WorktreeManager
	workers   workerruntime.Factory
}

func NewExecutionFactory(localDataRoot, workerExecutable string, rules []permissionbroker.ProcessRule) (*ExecutionFactory, error) {
	if localDataRoot == "" || workerExecutable == "" {
		return nil, executionruntime.ErrInvalidRequest
	}
	broker, err := permissionbroker.New(rules)
	if err != nil {
		return nil, fmt.Errorf("initialize permission broker: %w", err)
	}
	worktrees, err := gitcli.NewWorktreeManager(filepath.Join(localDataRoot, "task-runtime"))
	if err != nil {
		return nil, fmt.Errorf("initialize task worktree manager: %w", err)
	}
	workers, err := workerprocess.New(workerExecutable)
	if err != nil {
		return nil, fmt.Errorf("initialize worker process runtime: %w", err)
	}
	return &ExecutionFactory{broker: broker, worktrees: worktrees, workers: workers}, nil
}

func NewDefaultExecutionFactory(localDataRoot, workerExecutable string) (*ExecutionFactory, error) {
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		return nil, fmt.Errorf("resolve go verification tool: %w", err)
	}
	goExecutable, err = filepath.Abs(goExecutable)
	if err != nil {
		return nil, fmt.Errorf("resolve absolute go verification tool: %w", err)
	}
	return NewExecutionFactory(localDataRoot, workerExecutable, []permissionbroker.ProcessRule{{
		ID: "go-test", Executable: goExecutable, ArgumentPrefix: []string{"test"}, MaxArguments: 64,
		MaxTimeout: verificationTimeout, MaxOutputBytes: verificationOutputMax, Default: permission.OutcomeRequireReview,
	}})
}

func (f *ExecutionFactory) New(store executionruntime.Store) (executionruntime.Executor, error) {
	if f == nil || store == nil || f.broker == nil || f.worktrees == nil || f.workers == nil {
		return nil, executionruntime.ErrInvalidRequest
	}
	coordinator, err := executionruntime.New(store, f.broker, f.worktrees, f.workers)
	if err != nil {
		return nil, fmt.Errorf("initialize execution coordinator: %w", err)
	}
	return coordinator, nil
}

func (f *ExecutionFactory) NewPatchReview(store patchreview.Store) (*patchreview.Service, error) {
	if f == nil || f.worktrees == nil {
		return nil, patchreview.ErrInvalidRequest
	}
	return patchreview.New(store, f.worktrees, redaction.NewScanner())
}

func NewExecutionCoordinator(store executionruntime.Store, localDataRoot, workerExecutable string, rules []permissionbroker.ProcessRule) (executionruntime.Executor, error) {
	factory, err := NewExecutionFactory(localDataRoot, workerExecutable, rules)
	if err != nil {
		return nil, err
	}
	return factory.New(store)
}

func SiblingWorkerExecutable() (string, error) {
	applicationExecutable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve application executable: %w", err)
	}
	name := "talos-worker"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(filepath.Dir(applicationExecutable), name), nil
}
