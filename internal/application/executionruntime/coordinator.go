package executionruntime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/application/permissionbroker"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/execution"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/permission"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/worktree"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/executionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/repository"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/workerruntime"
)

var (
	ErrInvalidRequest   = errors.New("invalid execution request")
	ErrPermissionDenied = errors.New("process execution permission denied")
	ErrJournalFailed    = errors.New("execution journal update failed")
)

type Store interface {
	taskstore.Store
	executionstore.Store
}

type Executor interface {
	Execute(context.Context, Request) (Result, error)
}

type Factory interface {
	New(Store) (Executor, error)
}

type Evaluator interface {
	ResolveVerification(task.Record, task.VerificationCommand) (permissionbroker.ResolvedVerification, error)
	Evaluate(task.Record, task.ContractRevision, permission.ProcessIntent, []permission.Grant) permissionbroker.Evaluation
}

type Request struct {
	TaskID         string
	CommandIndex   int
	IdempotencyKey string
}

type Result struct {
	Outcome           permission.Outcome
	ReasonCode        string
	PermissionRequest permission.Request
	RunID             string
	AttemptID         string
	CallID            string
	Worktree          worktree.Record
	Tool              workerruntime.ToolResult
	ShutdownError     error
	Replayed          bool
}

type Coordinator struct {
	store     Store
	broker    Evaluator
	worktrees repository.WorktreeManager
	workers   workerruntime.Factory
	now       func() time.Time
}

func New(store Store, broker Evaluator, worktrees repository.WorktreeManager, workers workerruntime.Factory) (*Coordinator, error) {
	if store == nil || broker == nil || worktrees == nil || workers == nil {
		return nil, ErrInvalidRequest
	}
	return &Coordinator{store: store, broker: broker, worktrees: worktrees, workers: workers, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (c *Coordinator) Execute(ctx context.Context, request Request) (Result, error) {
	if ctx == nil || request.TaskID == "" || request.CommandIndex < 0 || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 96 {
		return Result{}, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	record, err := c.store.GetTask(ctx, request.TaskID)
	if err != nil {
		return Result{}, err
	}
	contract, err := c.store.GetTaskContract(ctx, record.ID, record.CurrentRevision)
	if err != nil {
		return Result{}, err
	}
	if request.CommandIndex >= len(contract.VerificationCommands) {
		return Result{}, ErrInvalidRequest
	}
	resolved, err := c.broker.ResolveVerification(record, contract.VerificationCommands[request.CommandIndex])
	if err != nil || resolved.Intent.TaskID != record.ID || resolved.Intent.WorkspaceRoot != record.WorkspaceRoot || resolved.Intent.Validate() != nil || !validEnvironment(resolved.Environment, resolved.Intent.EnvironmentNames) || !validWorkingDirectory(resolved.WorkingDirectory) {
		return Result{}, ErrInvalidRequest
	}
	workspaceHash, err := permission.WorkspaceHash(record.WorkspaceRoot)
	if err != nil {
		return Result{}, ErrInvalidRequest
	}
	grants, err := c.store.ListActivePermissionGrants(ctx, record.VaultID, workspaceHash)
	if err != nil {
		return Result{}, err
	}
	evaluation := c.broker.Evaluate(record, contract, resolved.Intent, grants)
	result := Result{Outcome: evaluation.Outcome, ReasonCode: evaluation.ReasonCode}
	if evaluation.Outcome == permission.OutcomeRequireReview {
		permissionRequest, err := c.store.CreatePermissionRequest(ctx, executionstore.CreatePermissionRequestInput{VaultID: record.VaultID, Intent: resolved.Intent, OccurredAt: c.now().UTC(), IdempotencyKey: request.IdempotencyKey + ":permission-request"})
		if err != nil {
			return result, fmt.Errorf("%w: %v", ErrJournalFailed, err)
		}
		result.PermissionRequest = permissionRequest
		return result, nil
	}
	if evaluation.Outcome == permission.OutcomeDeny || evaluation.Capability == nil {
		return result, ErrPermissionDenied
	}
	now := c.now().UTC()
	journal, err := c.store.PrepareAttempt(ctx, executionstore.PrepareAttemptInput{VaultID: record.VaultID, TaskID: record.ID, WorkspaceHash: workspaceHash, CapabilityHash: evaluation.Capability.IntentHash, GrantID: evaluation.MatchedGrantID, OccurredAt: now, IdempotencyKey: request.IdempotencyKey + ":prepare"})
	if err != nil {
		return result, fmt.Errorf("%w: %v", ErrJournalFailed, err)
	}
	result.RunID, result.AttemptID, result.CallID = journal.Run.ID, journal.Attempt.ID, journal.Attempt.CallID
	result.Replayed = journal.Replayed
	if journal.Replayed {
		return replayResult(result, journal)
	}
	ownedWorktree, err := c.worktrees.Create(ctx, repository.CreateWorktreeInput{TaskID: record.ID, RepositoryRoot: record.WorkspaceRoot, BaselineCommit: record.BaselineCommit, CreatedAt: now})
	if err != nil {
		finishErr := c.finish(ctx, record.VaultID, journal.Attempt.ID, journal.Run.ID, execution.AttemptFailed, execution.RunFailed, 0, "WORKTREE_CREATE_FAILED", request.IdempotencyKey)
		if finishErr != nil {
			return result, finishErr
		}
		return result, err
	}
	result.Worktree = ownedWorktree
	worker, err := c.workers.Start(ctx)
	if err != nil {
		_ = c.worktrees.Remove(context.WithoutCancel(ctx), ownedWorktree)
		finishErr := c.finish(ctx, record.VaultID, journal.Attempt.ID, journal.Run.ID, execution.AttemptFailed, execution.RunFailed, 0, "WORKER_START_FAILED", request.IdempotencyKey)
		if finishErr != nil {
			return result, finishErr
		}
		return result, err
	}
	capability := workerruntime.Capability{ID: evaluation.Capability.ID, Executable: evaluation.Capability.Executable, Arguments: append([]string(nil), evaluation.Capability.ArgumentPrefix...), EnvironmentNames: append([]string(nil), evaluation.Capability.EnvironmentNames...), Timeout: evaluation.Capability.MaxTimeout, MaxOutputBytes: evaluation.Capability.MaxOutputBytes}
	if err := worker.StartRun(ctx, workerruntime.RunPolicy{RunID: journal.Run.ID, WorktreeRoot: ownedWorktree.Root, Capabilities: []workerruntime.Capability{capability}}); err != nil {
		finishErr := c.finishKnownFailure(ctx, worker, record.VaultID, journal.Attempt.ID, journal.Run.ID, "WORKER_START_FAILED", request.IdempotencyKey)
		_ = c.worktrees.Remove(context.WithoutCancel(ctx), ownedWorktree)
		return result, finishErr
	}
	toolResult, toolErr := worker.RunTool(ctx, workerruntime.ToolRequest{RunID: journal.Run.ID, CallID: journal.Attempt.CallID, CapabilityID: capability.ID, Arguments: append([]string(nil), resolved.Intent.Arguments...), WorkingDirectory: resolved.WorkingDirectory, Environment: copyEnvironment(resolved.Environment), Timeout: resolved.Intent.Timeout, MaxOutputBytes: resolved.Intent.MaxOutputBytes})
	result.Tool = toolResult
	attemptState, runState, safeCode := classifyResult(toolResult, toolErr)
	finishErr := c.finish(ctx, record.VaultID, journal.Attempt.ID, journal.Run.ID, attemptState, runState, toolResult.ExitCode, safeCode, request.IdempotencyKey)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	result.ShutdownError = worker.Shutdown(shutdownCtx)
	cancel()
	if finishErr != nil {
		return result, finishErr
	}
	if toolErr != nil {
		return result, toolErr
	}
	return result, nil
}

func (c *Coordinator) finishKnownFailure(ctx context.Context, worker workerruntime.Session, vaultID, attemptID, runID, code, key string) error {
	finishErr := c.finish(ctx, vaultID, attemptID, runID, execution.AttemptFailed, execution.RunFailed, 0, code, key)
	_ = worker.Close()
	if finishErr != nil {
		return finishErr
	}
	return workerruntime.ErrExecutionFailed
}

func (c *Coordinator) finish(ctx context.Context, vaultID, attemptID, runID string, attemptState execution.AttemptState, runState execution.RunState, exitCode int, safeCode, key string) error {
	now := c.now().UTC()
	journalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var exit *int
	if attemptState == execution.AttemptSucceeded || attemptState == execution.AttemptFailed {
		exit = &exitCode
	}
	if _, err := c.store.FinishAttempt(journalCtx, executionstore.FinishAttemptInput{VaultID: vaultID, AttemptID: attemptID, ExpectedState: execution.AttemptDispatchPending, NextState: attemptState, ExitCode: exit, SafeErrorCode: safeCode, OccurredAt: now, IdempotencyKey: key + ":attempt-finish"}); err != nil {
		return fmt.Errorf("%w: %v", ErrJournalFailed, err)
	}
	if _, err := c.store.FinishRun(journalCtx, executionstore.FinishRunInput{VaultID: vaultID, RunID: runID, ExpectedState: execution.RunActive, NextState: runState, OccurredAt: now, IdempotencyKey: key + ":run-finish"}); err != nil {
		return fmt.Errorf("%w: %v", ErrJournalFailed, err)
	}
	return nil
}

func classifyResult(result workerruntime.ToolResult, err error) (execution.AttemptState, execution.RunState, string) {
	if err == nil && result.State == workerruntime.ToolSucceeded && result.ExitCode == 0 {
		return execution.AttemptSucceeded, execution.RunCompleted, ""
	}
	if errors.Is(err, workerruntime.ErrProtocol) || errors.Is(err, workerruntime.ErrUnavailable) {
		return execution.AttemptUnknown, execution.RunUnknown, "WORKER_OUTCOME_UNKNOWN"
	}
	if result.State == workerruntime.ToolCanceled {
		return execution.AttemptCanceled, execution.RunCanceled, "WORKER_CANCELED"
	}
	return execution.AttemptFailed, execution.RunFailed, "WORKER_EXECUTION_FAILED"
}

func replayResult(result Result, journal executionstore.Prepared) (Result, error) {
	switch journal.Attempt.State {
	case execution.AttemptSucceeded:
		result.Tool.State = workerruntime.ToolSucceeded
		if journal.Attempt.ExitCode != nil {
			result.Tool.ExitCode = *journal.Attempt.ExitCode
		}
		return result, nil
	case execution.AttemptCanceled:
		result.Tool.State = workerruntime.ToolCanceled
		return result, workerruntime.ErrExecutionFailed
	case execution.AttemptFailed:
		result.Tool.State = workerruntime.ToolFailed
		if journal.Attempt.ExitCode != nil {
			result.Tool.ExitCode = *journal.Attempt.ExitCode
		}
		return result, workerruntime.ErrExecutionFailed
	case execution.AttemptUnknown, execution.AttemptDispatchPending:
		result.Tool.State = workerruntime.ToolUnknown
		return result, workerruntime.ErrProtocol
	default:
		return result, ErrJournalFailed
	}
}

func validEnvironment(values map[string]string, allowed []string) bool {
	set := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		set[environmentKey(name)] = struct{}{}
	}
	for name := range values {
		if _, exists := set[environmentKey(name)]; !exists {
			return false
		}
	}
	return true
}

func environmentKey(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

func validWorkingDirectory(value string) bool {
	if strings.IndexByte(value, 0) >= 0 || filepath.IsAbs(value) || filepath.VolumeName(value) != "" {
		return false
	}
	clean := filepath.Clean(filepath.FromSlash(value))
	return clean == "." || (clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator)))
}

func copyEnvironment(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for name, value := range values {
		result[name] = value
	}
	return result
}
