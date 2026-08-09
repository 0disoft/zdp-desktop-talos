package memorycompile

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/decision"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspacemapping"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/decisionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorystore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
	"github.com/0disoft/zdp-desktop-talos/internal/security/redaction"
)

func TestCompileTaskCreatesOneIdempotentCandidatePerAnsweredDecision(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	taskRecord := task.Record{ID: "task-1", VaultID: "vault-1", WorkspaceRoot: t.TempDir(), BaselineCommit: strings.Repeat("a", 40), Status: task.StatusContracted, CurrentRevision: 2, CreatedAt: now, UpdatedAt: now, LastEventID: "task-event"}
	taskRecord.SourceWorkspaceHash = strings.Repeat("c", 64)
	taskRecord.WorkspaceID = workspacemapping.ID(taskRecord.VaultID, taskRecord.SourceWorkspaceHash)
	contract := task.ContractRevision{TaskID: taskRecord.ID, Revision: 2, BaselineCommit: taskRecord.BaselineCommit, Goal: "Keep Decision revisions consistent", AllowedPaths: []string{"internal/domain/decision/**"}, AcceptanceCriteria: []string{"tests pass"}, VerificationCommands: []task.VerificationCommand{{RuleID: "go-test", Arguments: []string{"test", "./..."}, WorkingDirectory: "."}}, Risk: task.RiskLow, CreatedAt: now, EventID: "contract-event"}
	answer := decision.Answer{ID: "answer-1", DecisionID: "decision-1", QuestionRevision: 1, ExpectedRepositoryRevision: taskRecord.BaselineCommit, SelectedOptionID: "strict", CreatedAt: now.Add(time.Minute), EventID: "answer-event"}
	result := decisionstore.Result{
		Decision: decision.Record{ID: "decision-1", VaultID: taskRecord.VaultID, TaskID: taskRecord.ID, QuestionRevision: 1, Category: decision.CategoryQuality, State: decision.StateAnswered, ExpectedRepositoryRevision: taskRecord.BaselineCommit, CreatedAt: now, UpdatedAt: answer.CreatedAt, LastEventID: answer.EventID},
		Question: decision.Question{DecisionID: "decision-1", QuestionRevision: 1, Question: "How should stale answers behave?", Reason: "The contract needs one concurrency rule.", RiskIfUnanswered: "Old answers may apply.", SafeDefault: decision.SafeDefault{Action: "reject stale"}, Options: []decision.Option{{ID: "strict", Label: "Reject stale answers", Consequence: "Require exact question revision."}, {ID: "latest", Label: "Use latest answer", Consequence: "Replace the previous answer."}}, CreatedAt: now, EventID: "question-event"},
		Answer:   &answer,
		Answers:  []decision.Answer{answer},
	}
	source := &fakeSource{task: taskRecord, contract: contract, decisions: []decisionstore.Result{result, {Decision: decision.Record{State: decision.StateOpen}}}}
	sink := &fakeMemorySink{}
	service, err := New(source, source, sink, redaction.NewScanner())
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.CompileTask(context.Background(), "vault-1", "task-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CompileTask(context.Background(), "vault-1", "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if first.Eligible != 1 || len(first.Memories) != 1 || second.Eligible != 1 || len(second.Memories) != 1 || len(sink.created) != 1 {
		t.Fatalf("first=%+v second=%+v created=%d", first, second, len(sink.created))
	}
	created := sink.created[0]
	if created.Kind != memory.KindDecision || created.Scope.WorkspaceID != taskRecord.WorkspaceID || created.Scope.SourceWorkspaceHash != taskRecord.SourceWorkspaceHash || created.Scope.WorkspaceRoot != "" || created.SourceActor != decisionSourceActor || created.Confidence != 85 || created.IdempotencyKey != "memory-decision:decision-1:1:answer-1" || !strings.Contains(created.Statement, "Reject stale answers") || len(created.EvidenceEventIDs) != 2 || len(created.Applicability.GoalTerms) == 0 {
		t.Fatalf("created=%+v", created)
	}
}

func TestCompileTaskRejectsCrossVaultAndMalformedEvidence(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	taskRecord := task.Record{ID: "task-1", VaultID: "vault-1", WorkspaceRoot: t.TempDir(), BaselineCommit: strings.Repeat("a", 40), Status: task.StatusContracted, CurrentRevision: 1, CreatedAt: now, UpdatedAt: now, LastEventID: "task-event"}
	taskRecord.SourceWorkspaceHash = strings.Repeat("d", 64)
	taskRecord.WorkspaceID = workspacemapping.ID(taskRecord.VaultID, taskRecord.SourceWorkspaceHash)
	contract := task.ContractRevision{TaskID: "task-1", Revision: 1, BaselineCommit: taskRecord.BaselineCommit, Goal: "memory decision", AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"tests pass"}, VerificationCommands: []task.VerificationCommand{{RuleID: "go-test", Arguments: []string{"test", "./..."}, WorkingDirectory: "."}}, Risk: task.RiskLow, CreatedAt: now, EventID: "contract-event"}
	for _, testCase := range []struct {
		name      string
		vaultID   string
		decisions []decisionstore.Result
	}{
		{name: "cross vault", vaultID: "vault-2"},
		{name: "same evidence event", vaultID: "vault-1", decisions: []decisionstore.Result{{Decision: decision.Record{ID: "d", VaultID: "vault-1", TaskID: "task-1", QuestionRevision: 1, State: decision.StateAnswered}, Question: decision.Question{DecisionID: "d", QuestionRevision: 1, Question: "q", Reason: "r", EventID: "same"}, Answer: &decision.Answer{ID: "a", DecisionID: "d", QuestionRevision: 1, Text: "answer", CreatedAt: now, EventID: "same"}}}},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			source := &fakeSource{task: taskRecord, contract: contract, decisions: testCase.decisions}
			service, _ := New(source, source, &fakeMemorySink{}, redaction.NewScanner())
			_, err := service.CompileTask(context.Background(), testCase.vaultID, "task-1")
			if !errors.Is(err, ErrInvalidSource) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestCompileTaskRejectsSecretBeforeCreatingAnyCandidate(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Add(-time.Hour)
	baseline := strings.Repeat("b", 40)
	taskRecord := task.Record{ID: "task-secret", VaultID: "vault-1", WorkspaceRoot: t.TempDir(), BaselineCommit: baseline, Status: task.StatusContracted, CurrentRevision: 1, CreatedAt: now, UpdatedAt: now, LastEventID: "task-event"}
	taskRecord.SourceWorkspaceHash = strings.Repeat("e", 64)
	taskRecord.WorkspaceID = workspacemapping.ID(taskRecord.VaultID, taskRecord.SourceWorkspaceHash)
	contract := task.ContractRevision{TaskID: taskRecord.ID, Revision: 1, BaselineCommit: baseline, Goal: "Remember concurrency decisions", AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"tests pass"}, VerificationCommands: []task.VerificationCommand{{RuleID: "go-test", Arguments: []string{"test", "./..."}, WorkingDirectory: "."}}, Risk: task.RiskLow, CreatedAt: now, EventID: "contract-event"}
	decisionResult := func(id, answerText string, createdAt time.Time) decisionstore.Result {
		answer := decision.Answer{ID: "answer-" + id, DecisionID: id, QuestionRevision: 1, ExpectedRepositoryRevision: baseline, Text: answerText, CreatedAt: createdAt, EventID: "answer-event-" + id}
		return decisionstore.Result{
			Decision: decision.Record{ID: id, VaultID: taskRecord.VaultID, TaskID: taskRecord.ID, QuestionRevision: 1, Category: decision.CategoryQuality, State: decision.StateAnswered, ExpectedRepositoryRevision: baseline, CreatedAt: now, UpdatedAt: createdAt, LastEventID: answer.EventID},
			Question: decision.Question{DecisionID: id, QuestionRevision: 1, Question: "Which revision policy should apply?", Reason: "The runtime needs a deterministic rule.", RiskIfUnanswered: "A stale answer may be reused.", SafeDefault: decision.SafeDefault{Action: "reject stale"}, Options: []decision.Option{{ID: "strict", Label: "Reject stale", Consequence: "Require an exact revision."}, {ID: "latest", Label: "Use latest", Consequence: "Replace prior answers."}}, CreatedAt: now, EventID: "question-event-" + id},
			Answer:   &answer, Answers: []decision.Answer{answer},
		}
	}
	source := &fakeSource{task: taskRecord, contract: contract, decisions: []decisionstore.Result{
		decisionResult("safe", "Require the exact question revision.", now.Add(time.Minute)),
		decisionResult("secret", "token=ghp_abcdefghijklmnopqrstuvwxyz123456", now.Add(2*time.Minute)),
	}}
	sink := &fakeMemorySink{}
	service, err := New(source, source, sink, redaction.NewScanner())
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CompileTask(context.Background(), taskRecord.VaultID, taskRecord.ID)
	if !errors.Is(err, ErrSensitiveSource) || len(sink.created) != 0 {
		t.Fatalf("err=%v created=%d", err, len(sink.created))
	}
}

type fakeSource struct {
	task      task.Record
	contract  task.ContractRevision
	decisions []decisionstore.Result
}

func (f *fakeSource) GetTask(context.Context, string) (task.Record, error) { return f.task, nil }
func (f *fakeSource) GetTaskContract(context.Context, string, int) (task.ContractRevision, error) {
	return f.contract, nil
}
func (f *fakeSource) ListDecisions(context.Context, string, string, int) ([]decisionstore.Result, error) {
	return append([]decisionstore.Result(nil), f.decisions...), nil
}

type fakeMemorySink struct {
	created []memorystore.CreateCandidateInput
	records map[string]memory.Record
}

func (f *fakeMemorySink) CreateMemoryCandidate(_ context.Context, input memorystore.CreateCandidateInput) (memory.Record, error) {
	if f.records == nil {
		f.records = map[string]memory.Record{}
	}
	if record, exists := f.records[input.IdempotencyKey]; exists {
		return record, nil
	}
	f.created = append(f.created, input)
	record := memory.Record{ID: "memory-1", VaultID: input.VaultID, State: memory.StateCandidate, Revision: 1}
	f.records[input.IdempotencyKey] = record
	return record, nil
}
func (f *fakeMemorySink) GetMemory(_ context.Context, _, _ string) (memory.Record, error) {
	for _, record := range f.records {
		return record, nil
	}
	return memory.Record{}, taskstore.ErrNotFound
}
