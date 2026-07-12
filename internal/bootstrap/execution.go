package bootstrap

import (
	"fmt"
	"path/filepath"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/gitcli"
	"github.com/0disoft/zdp-desktop-talos/internal/adapters/workerprocess"
	"github.com/0disoft/zdp-desktop-talos/internal/application/executionruntime"
	"github.com/0disoft/zdp-desktop-talos/internal/application/permissionbroker"
)

func NewExecutionCoordinator(store executionruntime.Store, localDataRoot, workerExecutable string, rules []permissionbroker.ProcessRule) (*executionruntime.Coordinator, error) {
	if store == nil || localDataRoot == "" || workerExecutable == "" {
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
	coordinator, err := executionruntime.New(store, broker, worktrees, workers)
	if err != nil {
		return nil, fmt.Errorf("initialize execution coordinator: %w", err)
	}
	return coordinator, nil
}
