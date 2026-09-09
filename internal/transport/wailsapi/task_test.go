package wailsapi

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspace"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
)

func TestTaskServiceReinspectsCleanBaselineAndPersistsContract(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0).UTC()
	snapshot := workspace.RepositorySnapshot{Root: `C:\repo`, BaselineCommit: strings.Repeat("a", 40), HeadRef: "main", CapturedAt: now}
	database := &serviceDatabase{taskCreated: taskstore.Created{
		Task:     task.Record{ID: "task-1", VaultID: "vault-1", WorkspaceRoot: snapshot.Root, BaselineCommit: snapshot.BaselineCommit, Status: task.StatusContracted, CurrentRevision: 1, CreatedAt: now, UpdatedAt: now, LastEventID: "event-1"},
		Contract: task.ContractRevision{TaskID: "task-1", Revision: 1, BaselineCommit: snapshot.BaselineCommit, Goal: "Implement contracts", AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"tests pass"}, Risk: task.RiskMedium, CreatedAt: now, EventID: "event-1"},
	}}
	vault := openTaskTestVault(t, database)
	workspaceService := NewWorkspaceService(&sequenceInspector{snapshots: []workspace.RepositorySnapshot{snapshot, snapshot}}, nil)
	if result := workspaceService.InspectRepository(snapshot.Root, "open"); result.Error != nil {
		t.Fatalf("open=%+v", result)
	}
	service := NewTaskService(vault, workspaceService)
	verification := []VerificationCommandRequest{{RuleID: "go-test", Arguments: []string{"test", "./internal/..."}, WorkingDirectory: "."}}
	result := service.CreateContract(TaskCreateRequest{Goal: "Implement contracts", AllowedPaths: []string{"internal/**"}, ForbiddenActions: []string{"git.push"}, AcceptanceCriteria: []string{"tests pass"}, VerificationCommands: verification, Risk: "medium", RequestID: "request-1", CorrelationID: "task-create"})
	if result.Error != nil || result.Task == nil || result.Task.TaskID != "task-1" || result.Task.BaselineCommit != snapshot.BaselineCommit {
		t.Fatalf("result=%+v", result)
	}
	if database.taskInput.WorkspaceRoot != snapshot.Root || database.taskInput.BaselineCommit != snapshot.BaselineCommit || database.taskInput.IdempotencyKey != "task-contract:request-1" {
		t.Fatalf("input=%+v", database.taskInput)
	}
	if len(database.taskInput.VerificationCommands) != 1 || database.taskInput.VerificationCommands[0].RuleID != "go-test" || database.taskInput.VerificationCommands[0].WorkingDirectory != "." {
		t.Fatalf("verification=%+v", database.taskInput.VerificationCommands)
	}
	listed := service.ListContracts("list-existing")
	if listed.Error != nil || len(listed.Tasks) != 1 || listed.Tasks[0].Task.TaskID != result.Task.TaskID {
		t.Fatalf("listed=%+v", listed)
	}
	database.taskCreated.Task.CurrentRevision = 2
	database.taskCreated.Task.UpdatedAt = now.Add(time.Minute)
	database.taskCreated.Task.LastEventID = "event-2"
	database.taskCreated.Contract.Revision = 2
	database.taskCreated.Contract.Goal = "Revised contracts"
	database.taskCreated.Contract.CreatedAt = now.Add(time.Minute)
	database.taskCreated.Contract.EventID = "event-2"
	revised := service.ReviseContract(TaskReviseRequest{TaskID: "task-1", ExpectedRevision: 1, Goal: "Revised contracts", AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"tests pass"}, VerificationCommands: verification, Risk: "medium", RequestID: "request-2", CorrelationID: "task-revise"})
	if revised.Error != nil || revised.Task == nil || revised.Task.Revision != 2 {
		t.Fatalf("revised=%+v", revised)
	}
	if database.reviseInput.TaskID != "task-1" || database.reviseInput.ExpectedRevision != 1 || database.reviseInput.IdempotencyKey != "task-contract-revision:request-2" {
		t.Fatalf("revise input=%+v", database.reviseInput)
	}
}

func TestTaskServiceRequiresVerificationCommand(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0).UTC()
	snapshot := workspace.RepositorySnapshot{Root: `C:\repo`, BaselineCommit: strings.Repeat("a", 40), HeadRef: "main", CapturedAt: now}
	database := &serviceDatabase{}
	vault := openTaskTestVault(t, database)
	workspaceService := NewWorkspaceService(&sequenceInspector{snapshots: []workspace.RepositorySnapshot{snapshot, snapshot}}, nil)
	if result := workspaceService.InspectRepository(snapshot.Root, "open"); result.Error != nil {
		t.Fatal(result.Error)
	}
	result := NewTaskService(vault, workspaceService).CreateContract(TaskCreateRequest{Goal: "goal", AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"pass"}, Risk: "low", RequestID: "request-1"})
	if result.Error == nil || result.Error.Code != "VAULT_INPUT_INVALID" {
		t.Fatalf("result=%+v", result)
	}
	if database.taskInput.VaultID != "" {
		t.Fatalf("persistence called: %+v", database.taskInput)
	}
}

func TestTaskServiceRejectsDirtyOrChangedWorkspaceBeforePersistence(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0).UTC()
	clean := workspace.RepositorySnapshot{Root: `C:\repo`, BaselineCommit: strings.Repeat("a", 40), HeadRef: "main", CapturedAt: now}
	dirty := clean
	dirty.Dirty = true
	dirty.Changes = []workspace.Change{{Path: "main.go", Kind: workspace.ChangeTracked, IndexStatus: 'M', WorktreeStatus: '.'}}
	changed := clean
	changed.BaselineCommit = strings.Repeat("b", 40)
	for _, testCase := range []struct {
		name    string
		current workspace.RepositorySnapshot
		code    string
	}{{"dirty", dirty, "WORKSPACE_DIRTY"}, {"changed", changed, "WORKSPACE_BASELINE_CHANGED"}} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			database := &serviceDatabase{}
			vault := openTaskTestVault(t, database)
			workspaceService := NewWorkspaceService(&sequenceInspector{snapshots: []workspace.RepositorySnapshot{clean, testCase.current}}, nil)
			if result := workspaceService.InspectRepository(clean.Root, "open"); result.Error != nil {
				t.Fatal(result.Error)
			}
			result := NewTaskService(vault, workspaceService).CreateContract(TaskCreateRequest{Goal: "goal", AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"pass"}, Risk: "low", RequestID: "request-1"})
			if result.Error == nil || result.Error.Code != testCase.code {
				t.Fatalf("result=%+v", result)
			}
			if database.taskInput.VaultID != "" {
				t.Fatalf("persistence called: %+v", database.taskInput)
			}
		})
	}
}

func openTaskTestVault(t *testing.T, database *serviceDatabase) *VaultService {
	t.Helper()
	creator, err := vaultbootstrap.NewCreator(&serviceKeyStore{}, &serviceDatabaseFactory{database: database}, &serviceCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	service := NewVaultService(creator, nil)
	if result := service.Create(30, "vault-create"); result.Error != nil {
		t.Fatalf("create Vault=%+v", result)
	}
	return service
}

type sequenceInspector struct {
	mu        sync.Mutex
	snapshots []workspace.RepositorySnapshot
	index     int
}

func (s *sequenceInspector) Inspect(context.Context, string) (workspace.RepositorySnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.index
	if index >= len(s.snapshots) {
		index = len(s.snapshots) - 1
	}
	s.index++
	return s.snapshots[index], nil
}
