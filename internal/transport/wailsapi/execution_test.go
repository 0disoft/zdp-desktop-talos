package wailsapi

import (
	"context"
	"errors"
	"testing"

	"github.com/0disoft/zdp-desktop-talos/internal/application/executionruntime"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/permission"
)

func TestExecutionServiceReturnsBoundedReviewStatus(t *testing.T) {
	t.Parallel()
	database := &serviceDatabase{}
	vault := openTaskTestVault(t, database)
	executor := &executionExecutorStub{result: executionruntime.Result{Outcome: permission.OutcomeRequireReview, PermissionRequest: permission.Request{ID: "review-1"}}}
	factory := &executionFactoryStub{executor: executor}
	service := NewExecutionService(vault, factory, nil)

	result := service.ExecuteVerification(ExecutionRequest{TaskID: "task-1", CommandIndex: 0, RequestID: "request-1", CorrelationID: "execution-1"})
	if result.Error != nil || result.Execution == nil || result.Execution.State != "review_required" || result.Execution.PermissionRequestID != "review-1" || result.Execution.Outcome != string(permission.OutcomeRequireReview) {
		t.Fatalf("result=%+v", result)
	}
	if !factory.storeSeen || executor.request.TaskID != "task-1" || executor.request.CommandIndex != 0 || executor.request.IdempotencyKey != "task-verification:request-1" {
		t.Fatalf("factory=%+v request=%+v", factory, executor.request)
	}
}

func TestExecutionServiceFailsClosedWithoutRuntimeOrVault(t *testing.T) {
	t.Parallel()
	unavailable := NewExecutionService(NewVaultService(nil, errors.New("storage unavailable")), nil, errors.New("worker unavailable"))
	result := unavailable.ExecuteVerification(ExecutionRequest{TaskID: "task-1", RequestID: "request-1", CorrelationID: "execution-1"})
	if result.Error == nil || result.Error.Code != "EXECUTION_UNAVAILABLE" || result.Error.Message == "worker unavailable" {
		t.Fatalf("unavailable=%+v", result)
	}

	locked := NewExecutionService(NewVaultService(nil, nil), &executionFactoryStub{executor: &executionExecutorStub{}}, nil)
	result = locked.ExecuteVerification(ExecutionRequest{TaskID: "task-1", RequestID: "request-1", CorrelationID: "execution-2"})
	if result.Error == nil || result.Error.Code != "VAULT_NOT_OPEN" {
		t.Fatalf("locked=%+v", result)
	}
}

type executionFactoryStub struct {
	executor  executionruntime.Executor
	err       error
	storeSeen bool
}

func (f *executionFactoryStub) New(store executionruntime.Store) (executionruntime.Executor, error) {
	f.storeSeen = store != nil
	return f.executor, f.err
}

type executionExecutorStub struct {
	request executionruntime.Request
	result  executionruntime.Result
	err     error
}

func (e *executionExecutorStub) Execute(_ context.Context, request executionruntime.Request) (executionruntime.Result, error) {
	e.request = request
	return e.result, e.err
}
