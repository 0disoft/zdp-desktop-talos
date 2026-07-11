package wailsapi

import (
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/decision"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspace"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/decisionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
)

func TestDecisionServiceCreatesListsAndAnswersAgainstCurrentBaseline(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0).UTC()
	snapshot := workspace.RepositorySnapshot{Root: `C:\repo`, BaselineCommit: strings.Repeat("a", 40), HeadRef: "main", CapturedAt: now}
	taskRecord := task.Record{ID: "task-1", VaultID: "placeholder", WorkspaceRoot: snapshot.Root, BaselineCommit: snapshot.BaselineCommit, Status: task.StatusContracted, CurrentRevision: 1, CreatedAt: now, UpdatedAt: now, LastEventID: "task-event"}
	decisionResult := decisionstore.Result{Decision: decision.Record{ID: "decision-1", VaultID: "placeholder", TaskID: taskRecord.ID, QuestionRevision: 1, Category: decision.CategoryBlocking, State: decision.StateOpen, ExpectedRepositoryRevision: snapshot.BaselineCommit, CreatedAt: now, UpdatedAt: now, LastEventID: "decision-event"}, Question: decision.Question{DecisionID: "decision-1", QuestionRevision: 1, Question: "Publish API?", Reason: "Compatibility", RiskIfUnanswered: "Cannot continue", SafeDefault: decision.SafeDefault{Action: "keep private"}, BlockingScopes: []string{"api.publish"}, Options: []decision.Option{{ID: "yes", Label: "Publish", Consequence: "Public"}, {ID: "no", Label: "Private", Consequence: "Private"}}, CreatedAt: now, EventID: "decision-event"}}
	database := &serviceDatabase{taskCreated: taskstore.Created{Task: taskRecord}, decisionResult: decisionResult, decisionList: []decisionstore.Result{decisionResult}}
	vault := openTaskTestVault(t, database)
	database.taskCreated.Task.VaultID = vault.Status().VaultID
	workspaceService := NewWorkspaceService(&sequenceInspector{snapshots: []workspace.RepositorySnapshot{snapshot, snapshot, snapshot}}, nil)
	if result := workspaceService.InspectRepository(snapshot.Root, "open"); result.Error != nil {
		t.Fatal(result.Error)
	}
	service := NewDecisionService(vault, workspaceService)
	created := service.Create(DecisionCreateRequest{TaskID: "task-1", Category: "blocking", Question: "Publish API?", Reason: "Compatibility", RiskIfUnanswered: "Cannot continue", SafeDefault: DecisionSafeDefaultDTO{Action: "keep private"}, BlockingScopes: []string{"api.publish"}, Options: []DecisionOptionDTO{{ID: "yes", Label: "Publish", Consequence: "Public"}, {ID: "no", Label: "Private", Consequence: "Private"}}, RequestID: "create-1", CorrelationID: "decision-create"})
	if created.Error != nil || created.Decision == nil || created.Decision.DecisionID != "decision-1" {
		t.Fatalf("created=%+v", created)
	}
	if database.decisionInput.ExpectedRepositoryRevision != snapshot.BaselineCommit || database.decisionInput.IdempotencyKey != "decision-create:create-1" {
		t.Fatalf("input=%+v", database.decisionInput)
	}
	listed := service.List("task-1", "decision-list")
	if listed.Error != nil || len(listed.Decisions) != 1 {
		t.Fatalf("listed=%+v", listed)
	}
	decisionResult.Decision.State = decision.StateAnswered
	decisionResult.Answer = &decision.Answer{ID: "answer-1", DecisionID: "decision-1", QuestionRevision: 1, ExpectedRepositoryRevision: snapshot.BaselineCommit, SelectedOptionID: "no", CreatedAt: now.Add(time.Minute), EventID: "answer-event"}
	database.decisionResult = decisionResult
	answered := service.Answer(DecisionAnswerRequest{DecisionID: "decision-1", QuestionRevision: 1, SelectedOptionID: "no", RequestID: "answer-1", CorrelationID: "decision-answer"})
	if answered.Error != nil || answered.Decision == nil || answered.Decision.State != "answered" {
		t.Fatalf("answered=%+v", answered)
	}
	if database.answerInput.ExpectedRepositoryRevision != snapshot.BaselineCommit || database.answerInput.IdempotencyKey != "decision-answer:answer-1" {
		t.Fatalf("answer input=%+v", database.answerInput)
	}
}

func TestDecisionAnswerAllowsDirtySameCommitButRejectsChangedCommit(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0).UTC()
	clean := workspace.RepositorySnapshot{Root: `C:\repo`, BaselineCommit: strings.Repeat("a", 40), HeadRef: "main", CapturedAt: now}
	dirty := clean
	dirty.Dirty = true
	dirty.Changes = []workspace.Change{{Path: "main.go", Kind: workspace.ChangeTracked, IndexStatus: 'M', WorktreeStatus: '.'}}
	changed := clean
	changed.BaselineCommit = strings.Repeat("b", 40)
	for _, testCase := range []struct {
		name     string
		current  workspace.RepositorySnapshot
		wantCode string
	}{{"dirty", dirty, ""}, {"changed", changed, "WORKSPACE_BASELINE_CHANGED"}} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			database := &serviceDatabase{decisionResult: decisionstore.Result{Decision: decision.Record{State: decision.StateAnswered}, Question: decision.Question{}, Answer: &decision.Answer{}}}
			vault := openTaskTestVault(t, database)
			workspaceService := NewWorkspaceService(&sequenceInspector{snapshots: []workspace.RepositorySnapshot{clean, testCase.current}}, nil)
			if result := workspaceService.InspectRepository(clean.Root, "open"); result.Error != nil {
				t.Fatal(result.Error)
			}
			result := NewDecisionService(vault, workspaceService).Answer(DecisionAnswerRequest{DecisionID: "decision-1", QuestionRevision: 1, SelectedOptionID: "yes", RequestID: "answer"})
			if testCase.wantCode == "" {
				if result.Error != nil {
					t.Fatalf("result=%+v", result)
				}
			} else if result.Error == nil || result.Error.Code != testCase.wantCode {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}
