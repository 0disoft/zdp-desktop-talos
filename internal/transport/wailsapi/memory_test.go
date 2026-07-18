package wailsapi

import (
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/decision"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspace"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/decisionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
)

func TestMemoryServiceCompilesReviewsAndExplainsTaskMemory(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Add(-time.Hour)
	root := `C:\repo`
	baseline := strings.Repeat("a", 40)
	database := &serviceDatabase{}
	creator, err := vaultbootstrap.NewCreator(&serviceKeyStore{}, &serviceDatabaseFactory{database: database}, &serviceCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	vaultService := NewVaultService(creator, nil)
	createdVault := vaultService.Create(30, "create-vault")
	if createdVault.Error != nil || createdVault.Vault == nil {
		t.Fatalf("vault=%+v", createdVault)
	}
	vaultID := createdVault.Vault.VaultID
	database.taskCreated = taskstore.Created{
		Task:     task.Record{ID: "task-1", VaultID: vaultID, WorkspaceRoot: root, BaselineCommit: baseline, Status: task.StatusContracted, CurrentRevision: 1, CreatedAt: now, UpdatedAt: now, LastEventID: "task-event"},
		Contract: task.ContractRevision{TaskID: "task-1", Revision: 1, BaselineCommit: baseline, Goal: "Keep Decision revisions consistent", AllowedPaths: []string{"internal/domain/decision/**"}, AcceptanceCriteria: []string{"tests pass"}, VerificationCommands: []task.VerificationCommand{{RuleID: "go-test", Arguments: []string{"test", "./..."}, WorkingDirectory: "."}}, Risk: task.RiskLow, CreatedAt: now, EventID: "contract-event"},
	}
	answer := decision.Answer{ID: "answer-1", DecisionID: "decision-1", QuestionRevision: 1, ExpectedRepositoryRevision: baseline, SelectedOptionID: "strict", CreatedAt: now.Add(time.Minute), EventID: "answer-event"}
	database.decisionList = []decisionstore.Result{{
		Decision: decision.Record{ID: "decision-1", VaultID: vaultID, TaskID: "task-1", QuestionRevision: 1, Category: decision.CategoryQuality, State: decision.StateAnswered, ExpectedRepositoryRevision: baseline, CreatedAt: now, UpdatedAt: answer.CreatedAt, LastEventID: answer.EventID},
		Question: decision.Question{DecisionID: "decision-1", QuestionRevision: 1, Question: "How should stale answers behave?", Reason: "The contract needs one concurrency rule.", RiskIfUnanswered: "Old answers may apply.", SafeDefault: decision.SafeDefault{Action: "reject stale"}, Options: []decision.Option{{ID: "strict", Label: "Reject stale answers", Consequence: "Require exact question revision."}, {ID: "latest", Label: "Use latest answer", Consequence: "Replace older answers."}}, CreatedAt: now, EventID: "question-event"},
		Answer:   &answer, Answers: []decision.Answer{answer},
	}}
	workspaceService := NewWorkspaceService(fakeInspector{snapshot: workspace.RepositorySnapshot{Root: root, BaselineCommit: baseline, CapturedAt: now}}, nil)
	if opened := workspaceService.InspectRepository(root, "open"); opened.Error != nil {
		t.Fatalf("workspace=%+v", opened)
	}
	service := NewMemoryService(vaultService, workspaceService)

	compiled := service.CompileTask(MemoryCompileRequest{TaskID: "task-1", RequestID: "compile-1", CorrelationID: "compile"})
	if compiled.Error != nil || compiled.Eligible != 1 || len(compiled.Memories) != 1 || compiled.Memories[0].State != string(memory.StateCandidate) || len(database.memoryInputs) != 1 {
		t.Fatalf("compiled=%+v inputs=%+v", compiled, database.memoryInputs)
	}
	repeated := service.CompileTask(MemoryCompileRequest{TaskID: "task-1", RequestID: "compile-2", CorrelationID: "repeat"})
	if repeated.Error != nil || len(repeated.Memories) != 1 || len(database.memoryInputs) != 1 {
		t.Fatalf("repeated=%+v inputs=%d", repeated, len(database.memoryInputs))
	}
	listed := service.ListCandidates("list")
	if listed.Error != nil || len(listed.Memories) != 1 || listed.Memories[0].EvidenceEventIDs[1] != "answer-event" || listed.Memories[0].Sensitivity != string(event.SensitivityPrivate) {
		t.Fatalf("listed=%+v", listed)
	}
	reviewed := service.Review(MemoryReviewRequest{MemoryID: listed.Memories[0].MemoryID, ExpectedRevision: 1, Outcome: string(memory.StateApproved), Reason: "사용자 결정과 일치함", RequestID: "review-1", CorrelationID: "review"})
	if reviewed.Error != nil || reviewed.Memory == nil || reviewed.Memory.State != string(memory.StateApproved) || reviewed.Memory.Revision != 2 {
		t.Fatalf("reviewed=%+v", reviewed)
	}
	contextResult := service.ExplainCurrentTask("task-1", "context")
	if contextResult.Error != nil || contextResult.Considered != 1 || len(contextResult.Items) != 1 || contextResult.Items[0].MemoryID != reviewed.Memory.MemoryID || !strings.Contains(contextResult.Items[0].Reason, "goal term") {
		t.Fatalf("context=%+v", contextResult)
	}
}

func TestMemoryServiceRejectsStaleWorkspaceAndInvalidOutcome(t *testing.T) {
	t.Parallel()
	service := NewMemoryService(nil, nil)
	if result := service.CompileTask(MemoryCompileRequest{}); result.Error == nil || result.Error.Code != "MEMORY_COMPILATION_INVALID" {
		t.Fatalf("compile=%+v", result)
	}
	service = NewMemoryService(&VaultService{}, nil)
	if result := service.Review(MemoryReviewRequest{MemoryID: "memory", ExpectedRevision: 1, Outcome: "stable", Reason: "reason", RequestID: "request"}); result.Error == nil || result.Error.Code != "MEMORY_REVIEW_REQUIRED" {
		t.Fatalf("review=%+v", result)
	}
}
