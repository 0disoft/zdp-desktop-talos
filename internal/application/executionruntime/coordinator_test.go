package executionruntime

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
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

func TestCoordinatorReviewPersistsRequestWithoutDispatchSideEffects(t *testing.T) {
	fixture := newCoordinatorFixture(t, permission.OutcomeRequireReview)
	result, err := fixture.coordinator.Execute(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != permission.OutcomeRequireReview || result.PermissionRequest.ID == "" || fixture.worktrees.created || fixture.workers.started || strings.Join(fixture.store.order, "|") != "permission-request" {
		t.Fatalf("result=%+v worktree=%v worker=%v order=%v", result, fixture.worktrees.created, fixture.workers.started, fixture.store.order)
	}
}

func TestCoordinatorJournalsBeforeDispatchAndFinishesKnownSuccess(t *testing.T) {
	fixture := newCoordinatorFixture(t, permission.OutcomeAllowTask)
	result, err := fixture.coordinator.Execute(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != permission.OutcomeAllowTask || result.Tool.State != workerruntime.ToolSucceeded || result.Worktree.Root == "" {
		t.Fatalf("result=%+v", result)
	}
	want := []string{"prepare", "worker-start-run", "worker-run-tool", "finish-attempt:succeeded", "finish-run:completed", "worker-shutdown"}
	if strings.Join(fixture.store.order, "|") != strings.Join(want, "|") {
		t.Fatalf("order=%v want=%v", fixture.store.order, want)
	}
	if fixture.worktrees.removed {
		t.Fatal("successful worktree was removed before review")
	}
}

func TestCoordinatorReplaysTerminalAttemptWithoutRepeatingSideEffects(t *testing.T) {
	fixture := newCoordinatorFixture(t, permission.OutcomeAllowTask)
	if _, err := fixture.coordinator.Execute(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	created, started := fixture.worktrees.createCount, fixture.workers.startCount
	replayed, err := fixture.coordinator.Execute(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replayed || replayed.Tool.State != workerruntime.ToolSucceeded || fixture.worktrees.createCount != created || fixture.workers.startCount != started {
		t.Fatalf("replayed=%+v worktrees=%d workers=%d", replayed, fixture.worktrees.createCount, fixture.workers.startCount)
	}
}

func TestCoordinatorRecordsUnknownWhenWorkerOutcomeIsAmbiguous(t *testing.T) {
	fixture := newCoordinatorFixture(t, permission.OutcomeAllowTask)
	fixture.worker.runErr = workerruntime.ErrProtocol
	_, err := fixture.coordinator.Execute(context.Background(), fixture.request)
	if !errors.Is(err, workerruntime.ErrProtocol) {
		t.Fatalf("error=%v", err)
	}
	if fixture.store.attemptFinish.NextState != execution.AttemptUnknown || fixture.store.runFinish.NextState != execution.RunUnknown || fixture.store.attemptFinish.SafeErrorCode != "WORKER_OUTCOME_UNKNOWN" {
		t.Fatalf("attempt=%+v run=%+v", fixture.store.attemptFinish, fixture.store.runFinish)
	}
}

func TestCoordinatorCleansWorktreeWhenWorkerPolicyCannotStart(t *testing.T) {
	fixture := newCoordinatorFixture(t, permission.OutcomeAllowTask)
	fixture.worker.startErr = workerruntime.ErrExecutionFailed
	_, err := fixture.coordinator.Execute(context.Background(), fixture.request)
	if !errors.Is(err, workerruntime.ErrExecutionFailed) {
		t.Fatalf("error=%v", err)
	}
	if !fixture.worktrees.removed || fixture.store.attemptFinish.NextState != execution.AttemptFailed || fixture.store.runFinish.NextState != execution.RunFailed {
		t.Fatalf("removed=%v attempt=%+v run=%+v", fixture.worktrees.removed, fixture.store.attemptFinish, fixture.store.runFinish)
	}
}

func TestCoordinatorRejectsUnknownContractCommandBeforePermissionOrDispatch(t *testing.T) {
	fixture := newCoordinatorFixture(t, permission.OutcomeAllowTask)
	fixture.request.CommandIndex = 1
	_, err := fixture.coordinator.Execute(context.Background(), fixture.request)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error=%v", err)
	}
	if len(fixture.store.order) != 0 || fixture.worktrees.created || fixture.workers.started {
		t.Fatalf("order=%v worktree=%v worker=%v", fixture.store.order, fixture.worktrees.created, fixture.workers.started)
	}
}

type coordinatorFixture struct {
	coordinator *Coordinator
	request     Request
	store       *coordinatorStore
	worktrees   *coordinatorWorktrees
	workers     *coordinatorWorkerFactory
	worker      *coordinatorWorker
}

func newCoordinatorFixture(t *testing.T, defaultOutcome permission.Outcome) coordinatorFixture {
	t.Helper()
	now := time.Date(2026, 7, 12, 1, 0, 0, 0, time.UTC)
	root := filepath.Join(t.TempDir(), "repo")
	executable := filepath.Join(t.TempDir(), "tool.exe")
	taskID := "0198a58c-7b00-7000-8000-000000000001"
	record := task.Record{ID: taskID, VaultID: "vault-1", WorkspaceRoot: root, BaselineCommit: strings.Repeat("a", 40), Status: task.StatusContracted, CurrentRevision: 1, CreatedAt: now, UpdatedAt: now, LastEventID: "task-event"}
	command := task.VerificationCommand{RuleID: "go-test", Arguments: []string{"test", "./..."}, WorkingDirectory: "."}
	contract := task.ContractRevision{TaskID: taskID, Revision: 1, BaselineCommit: record.BaselineCommit, Goal: "run tests", AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"tests pass"}, VerificationCommands: []task.VerificationCommand{command}, Risk: task.RiskLow, CreatedAt: now, EventID: "contract-event"}
	store := &coordinatorStore{record: record, contract: contract}
	worktrees := &coordinatorWorktrees{record: worktree.Record{TaskID: taskID, RepositoryRoot: root, Root: filepath.Join(t.TempDir(), "worktree"), BaselineCommit: record.BaselineCommit, CreatedAt: now}}
	worker := &coordinatorWorker{order: &store.order, result: workerruntime.ToolResult{State: workerruntime.ToolSucceeded, ExitCode: 0, StartedAt: now, FinishedAt: now.Add(time.Second)}}
	workers := &coordinatorWorkerFactory{worker: worker}
	broker, err := permissionbroker.New([]permissionbroker.ProcessRule{{ID: "go-test", Executable: executable, ArgumentPrefix: []string{"test"}, MaxArguments: 2, EnvironmentNames: []string{"CI"}, MaxTimeout: time.Minute, MaxOutputBytes: 2048, Default: defaultOutcome}})
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := New(store, broker, worktrees, workers)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.now = func() time.Time { return now }
	request := Request{TaskID: taskID, CommandIndex: 0, IdempotencyKey: "execute-1"}
	return coordinatorFixture{coordinator: coordinator, request: request, store: store, worktrees: worktrees, workers: workers, worker: worker}
}

type coordinatorStore struct {
	record        task.Record
	contract      task.ContractRevision
	order         []string
	attemptFinish executionstore.FinishAttemptInput
	runFinish     executionstore.FinishRunInput
	prepared      executionstore.Prepared
}

func (s *coordinatorStore) GetTask(context.Context, string) (task.Record, error) {
	return s.record, nil
}
func (s *coordinatorStore) GetTaskContract(context.Context, string, int) (task.ContractRevision, error) {
	return s.contract, nil
}
func (*coordinatorStore) CreateTaskContract(context.Context, taskstore.CreateInput) (taskstore.Created, error) {
	return taskstore.Created{}, errors.New("not used")
}
func (*coordinatorStore) ReviseTaskContract(context.Context, taskstore.ReviseInput) (taskstore.Created, error) {
	return taskstore.Created{}, errors.New("not used")
}
func (*coordinatorStore) SavePermissionGrant(context.Context, executionstore.SaveGrantInput) (permission.Grant, error) {
	return permission.Grant{}, errors.New("not used")
}
func (s *coordinatorStore) CreatePermissionRequest(_ context.Context, input executionstore.CreatePermissionRequestInput) (permission.Request, error) {
	s.order = append(s.order, "permission-request")
	workspaceHash, _ := permission.WorkspaceHash(input.Intent.WorkspaceRoot)
	capabilityHash, _ := permission.IntentHash(input.Intent)
	return permission.Request{ID: "permission-request-1", VaultID: input.VaultID, TaskID: input.Intent.TaskID, WorkspaceHash: workspaceHash, CapabilityHash: capabilityHash, Intent: input.Intent, State: permission.RequestOpen, CreatedAt: input.OccurredAt, UpdatedAt: input.OccurredAt, LastEventID: "permission-request-event"}, nil
}
func (*coordinatorStore) ListOpenPermissionRequests(context.Context, string, string, int) ([]permission.Request, error) {
	return nil, errors.New("not used")
}
func (*coordinatorStore) ResolvePermissionRequest(context.Context, executionstore.ResolvePermissionRequestInput) (executionstore.PermissionResolution, error) {
	return executionstore.PermissionResolution{}, errors.New("not used")
}
func (*coordinatorStore) ListActivePermissionGrants(context.Context, string, string) ([]permission.Grant, error) {
	return nil, nil
}
func (s *coordinatorStore) PrepareAttempt(_ context.Context, input executionstore.PrepareAttemptInput) (executionstore.Prepared, error) {
	s.order = append(s.order, "prepare")
	if s.prepared.Attempt.ID != "" {
		replayed := s.prepared
		replayed.Replayed = true
		return replayed, nil
	}
	now := input.OccurredAt
	run := execution.Run{ID: "run-1", VaultID: input.VaultID, TaskID: input.TaskID, WorkspaceHash: input.WorkspaceHash, State: execution.RunActive, CreatedAt: now, UpdatedAt: now, LastEventID: "run-event"}
	attempt := execution.Attempt{ID: "attempt-1", RunID: run.ID, CallID: "call-1", CapabilityHash: input.CapabilityHash, State: execution.AttemptDispatchPending, CreatedAt: now, UpdatedAt: now, LastEventID: "attempt-event"}
	s.prepared = executionstore.Prepared{Run: run, Attempt: attempt}
	return s.prepared, nil
}
func (s *coordinatorStore) FinishAttempt(_ context.Context, input executionstore.FinishAttemptInput) (execution.Attempt, error) {
	s.attemptFinish = input
	s.prepared.Attempt.State = input.NextState
	s.prepared.Attempt.ExitCode = input.ExitCode
	s.prepared.Attempt.SafeErrorCode = input.SafeErrorCode
	s.order = append(s.order, "finish-attempt:"+string(input.NextState))
	return execution.Attempt{}, nil
}
func (s *coordinatorStore) FinishRun(_ context.Context, input executionstore.FinishRunInput) (execution.Run, error) {
	s.runFinish = input
	s.prepared.Run.State = input.NextState
	s.order = append(s.order, "finish-run:"+string(input.NextState))
	return execution.Run{}, nil
}
func (*coordinatorStore) ReconcilePendingAttempts(context.Context, string, time.Time) (int, error) {
	return 0, nil
}

type coordinatorWorktrees struct {
	record      worktree.Record
	created     bool
	removed     bool
	createCount int
}

func (w *coordinatorWorktrees) Create(context.Context, repository.CreateWorktreeInput) (worktree.Record, error) {
	w.created = true
	w.createCount++
	return w.record, nil
}
func (w *coordinatorWorktrees) Remove(context.Context, worktree.Record) error {
	w.removed = true
	return nil
}

type coordinatorWorkerFactory struct {
	worker     *coordinatorWorker
	started    bool
	startCount int
}

func (f *coordinatorWorkerFactory) Start(context.Context) (workerruntime.Session, error) {
	f.started = true
	f.startCount++
	return f.worker, nil
}

type coordinatorWorker struct {
	order    *[]string
	startErr error
	runErr   error
	result   workerruntime.ToolResult
}

func (w *coordinatorWorker) StartRun(context.Context, workerruntime.RunPolicy) error {
	*w.order = append(*w.order, "worker-start-run")
	return w.startErr
}
func (w *coordinatorWorker) RunTool(context.Context, workerruntime.ToolRequest) (workerruntime.ToolResult, error) {
	*w.order = append(*w.order, "worker-run-tool")
	return w.result, w.runErr
}
func (w *coordinatorWorker) Shutdown(context.Context) error {
	*w.order = append(*w.order, "worker-shutdown")
	return nil
}
func (*coordinatorWorker) Close() error { return nil }
