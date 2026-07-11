package sqliteevent

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/decision"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/decisionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
)

func TestDecisionQuestionAndAnswersAreEncryptedRevisionBoundAndConflictPreserving(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "vault.db")
	store := openTestStore(t, path)
	taskRecord := createDecisionTestTask(t, store)
	marker := "private-decision-question-marker"
	created, err := store.CreateDecision(ctx, decisionstore.CreateInput{VaultID: "vault-alpha", TaskID: taskRecord.ID, Category: decision.CategoryBlocking, ExpectedRepositoryRevision: taskRecord.BaselineCommit, Question: marker, Reason: "public API cannot be undone", RiskIfUnanswered: "compatibility may break", SafeDefault: decision.SafeDefault{Action: "keep private", ContinuableScopes: []string{"tests.run"}}, BlockingScopes: []string{"api.publish"}, Options: []decision.Option{{ID: "publish", Label: "Publish", Consequence: "API becomes public"}, {ID: "private", Label: "Keep private", Consequence: "No public contract"}}, IdempotencyKey: "decision-create"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Decision.State != decision.StateOpen || created.Question.Question != marker {
		t.Fatalf("created=%+v", created)
	}
	firstInput := decisionstore.AnswerInput{VaultID: "vault-alpha", DecisionID: created.Decision.ID, QuestionRevision: 1, ExpectedRepositoryRevision: taskRecord.BaselineCommit, SelectedOptionID: "private", IdempotencyKey: "answer-private"}
	first, err := store.AnswerDecision(ctx, firstInput)
	if err != nil || first.Decision.State != decision.StateAnswered || first.Answer == nil {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	beforeEquivalentEvents := tableCount(t, store, "events")
	equivalent := firstInput
	equivalent.IdempotencyKey = "answer-private-equivalent"
	converged, err := store.AnswerDecision(ctx, equivalent)
	if err != nil || converged.Answer == nil || converged.Answer.ID != first.Answer.ID {
		t.Fatalf("converged=%+v err=%v", converged, err)
	}
	if got := tableCount(t, store, "events"); got != beforeEquivalentEvents {
		t.Fatalf("equivalent answer appended event: %d != %d", got, beforeEquivalentEvents)
	}
	second, err := store.AnswerDecision(ctx, decisionstore.AnswerInput{VaultID: "vault-alpha", DecisionID: created.Decision.ID, QuestionRevision: 1, ExpectedRepositoryRevision: taskRecord.BaselineCommit, SelectedOptionID: "publish", IdempotencyKey: "answer-publish"})
	if err != nil || second.Decision.State != decision.StateConflicted || second.Answer == nil {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	if got := tableCount(t, store, "decision_answers"); got != 2 {
		t.Fatalf("answers=%d", got)
	}
	replayedFirst, err := store.AnswerDecision(ctx, firstInput)
	if err != nil || replayedFirst.Decision.State != decision.StateAnswered || replayedFirst.Answer.ID != first.Answer.ID {
		t.Fatalf("replayed=%+v err=%v", replayedFirst, err)
	}
	current, err := store.GetDecision(ctx, created.Decision.ID)
	if err != nil || current.Decision.State != decision.StateConflicted {
		t.Fatalf("current=%+v err=%v", current, err)
	}
	if err := store.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(marker)) || bytes.Contains(data, []byte("public API cannot be undone")) {
		t.Fatal("decision body leaked into SQLite plaintext")
	}
	reopened := openTestStore(t, path)
	defer reopened.Close()
	restored, err := reopened.GetDecision(ctx, created.Decision.ID)
	if err != nil || restored.Decision.State != decision.StateConflicted || restored.Question.Question != marker {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
}

func TestStaleDecisionAnswersDoNotAppendEvents(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	defer store.Close()
	taskRecord := createDecisionTestTask(t, store)
	created := createDecisionTestQuestion(t, store, taskRecord)
	before := tableCount(t, store, "events")
	for _, testCase := range []struct {
		name       string
		revision   int
		repository string
		expected   error
	}{{"question", 2, taskRecord.BaselineCommit, decisionstore.ErrQuestionStale}, {"repository", 1, strings.Repeat("b", 40), decisionstore.ErrRepositoryStale}} {
		_, err := store.AnswerDecision(context.Background(), decisionstore.AnswerInput{VaultID: "vault-alpha", DecisionID: created.Decision.ID, QuestionRevision: testCase.revision, ExpectedRepositoryRevision: testCase.repository, SelectedOptionID: "yes", IdempotencyKey: "stale-" + testCase.name})
		if !errors.Is(err, testCase.expected) {
			t.Fatalf("%s error=%v", testCase.name, err)
		}
	}
	if got := tableCount(t, store, "events"); got != before {
		t.Fatalf("stale answers appended events: %d != %d", got, before)
	}
	if got := tableCount(t, store, "decision_answers"); got != 0 {
		t.Fatalf("stale answers=%d", got)
	}
}

func TestConcurrentDifferentDecisionAnswersProduceConflict(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	defer store.Close()
	taskRecord := createDecisionTestTask(t, store)
	created := createDecisionTestQuestion(t, store, taskRecord)
	start := make(chan struct{})
	results := make(chan decision.State, 2)
	errorsFound := make(chan error, 2)
	var group sync.WaitGroup
	for _, option := range []string{"yes", "no"} {
		option := option
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			result, err := store.AnswerDecision(context.Background(), decisionstore.AnswerInput{VaultID: "vault-alpha", DecisionID: created.Decision.ID, QuestionRevision: 1, ExpectedRepositoryRevision: taskRecord.BaselineCommit, SelectedOptionID: option, IdempotencyKey: "answer-" + option})
			if err != nil {
				errorsFound <- err
				return
			}
			results <- result.Decision.State
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
	answered, conflicted := 0, 0
	for state := range results {
		if state == decision.StateAnswered {
			answered++
		} else if state == decision.StateConflicted {
			conflicted++
		}
	}
	if answered != 1 || conflicted != 1 {
		t.Fatalf("answered=%d conflicted=%d", answered, conflicted)
	}
}

func TestDecisionConflictResolutionPreservesAnswers(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	defer store.Close()
	taskRecord := createDecisionTestTask(t, store)
	created := createDecisionTestQuestion(t, store, taskRecord)
	first, err := store.AnswerDecision(context.Background(), decisionstore.AnswerInput{VaultID: "vault-alpha", DecisionID: created.Decision.ID, QuestionRevision: 1, ExpectedRepositoryRevision: taskRecord.BaselineCommit, SelectedOptionID: "yes", IdempotencyKey: "answer-yes"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AnswerDecision(context.Background(), decisionstore.AnswerInput{VaultID: "vault-alpha", DecisionID: created.Decision.ID, QuestionRevision: 1, ExpectedRepositoryRevision: taskRecord.BaselineCommit, SelectedOptionID: "no", IdempotencyKey: "answer-no"}); err != nil {
		t.Fatal(err)
	}
	resolved, err := store.ResolveDecisionConflict(context.Background(), decisionstore.ResolveInput{VaultID: "vault-alpha", DecisionID: created.Decision.ID, QuestionRevision: 1, ExpectedRepositoryRevision: taskRecord.BaselineCommit, SelectedAnswerID: first.Answer.ID, IdempotencyKey: "resolve-yes"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Decision.State != decision.StateAnswered || resolved.Answer == nil || resolved.Answer.ID != first.Answer.ID || len(resolved.Answers) != 2 {
		t.Fatalf("resolved=%+v", resolved)
	}
	if got := tableCount(t, store, "decision_answers"); got != 2 {
		t.Fatalf("answers=%d", got)
	}
	before := tableCount(t, store, "events")
	replayed, err := store.ResolveDecisionConflict(context.Background(), decisionstore.ResolveInput{VaultID: "vault-alpha", DecisionID: created.Decision.ID, QuestionRevision: 1, ExpectedRepositoryRevision: taskRecord.BaselineCommit, SelectedAnswerID: first.Answer.ID, IdempotencyKey: "resolve-yes"})
	if err != nil || replayed.Answer == nil || replayed.Answer.ID != first.Answer.ID {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
	if got := tableCount(t, store, "events"); got != before {
		t.Fatalf("resolution replay appended event: %d != %d", got, before)
	}
}

func TestSupersededDecisionUsesRevisionScopedAnswers(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	defer store.Close()
	taskRecord := createDecisionTestTask(t, store)
	created := createDecisionTestQuestion(t, store, taskRecord)
	v1Input := decisionstore.AnswerInput{VaultID: "vault-alpha", DecisionID: created.Decision.ID, QuestionRevision: 1, ExpectedRepositoryRevision: taskRecord.BaselineCommit, SelectedOptionID: "yes", IdempotencyKey: "answer-v1"}
	if _, err := store.AnswerDecision(context.Background(), v1Input); err != nil {
		t.Fatal(err)
	}
	superseded, err := store.SupersedeDecision(context.Background(), decisionstore.SupersedeInput{VaultID: "vault-alpha", DecisionID: created.Decision.ID, ExpectedQuestionRevision: 1, ExpectedRepositoryRevision: taskRecord.BaselineCommit, Question: "Proceed now?", Reason: "Context changed", RiskIfUnanswered: "Still blocked", SafeDefault: decision.SafeDefault{Action: "wait"}, BlockingScopes: []string{"step.execute"}, Options: []decision.Option{{ID: "yes", Label: "Yes", Consequence: "Proceed"}, {ID: "no", Label: "No", Consequence: "Stop"}}, IdempotencyKey: "supersede-v2"})
	if err != nil {
		t.Fatal(err)
	}
	if superseded.Decision.QuestionRevision != 2 || superseded.Decision.State != decision.StateOpen || len(superseded.Answers) != 0 {
		t.Fatalf("superseded=%+v", superseded)
	}
	v2, err := store.AnswerDecision(context.Background(), decisionstore.AnswerInput{VaultID: "vault-alpha", DecisionID: created.Decision.ID, QuestionRevision: 2, ExpectedRepositoryRevision: taskRecord.BaselineCommit, SelectedOptionID: "yes", IdempotencyKey: "answer-v2"})
	if err != nil {
		t.Fatal(err)
	}
	if v2.Decision.State != decision.StateAnswered || v2.Answer == nil || len(v2.Answers) != 1 {
		t.Fatalf("v2=%+v", v2)
	}
	if got := tableCount(t, store, "decision_answers"); got != 2 {
		t.Fatalf("revision answers=%d", got)
	}
	replayedV1, err := store.AnswerDecision(context.Background(), v1Input)
	if err != nil || replayedV1.Decision.QuestionRevision != 1 || replayedV1.Question.QuestionRevision != 1 || replayedV1.Answer == nil || replayedV1.Answer.QuestionRevision != 1 {
		t.Fatalf("replayed v1=%+v err=%v", replayedV1, err)
	}
	if _, err = store.AnswerDecision(context.Background(), decisionstore.AnswerInput{VaultID: "vault-alpha", DecisionID: created.Decision.ID, QuestionRevision: 1, ExpectedRepositoryRevision: taskRecord.BaselineCommit, SelectedOptionID: "no", IdempotencyKey: "stale-v1"}); !errors.Is(err, decisionstore.ErrQuestionStale) {
		t.Fatalf("stale err=%v", err)
	}
}

func createDecisionTestTask(t *testing.T, store *Store) task.Record {
	t.Helper()
	ctx := context.Background()
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: "vault-alpha", RetentionDays: 30, IdempotencyKey: "vault-alpha"}); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateTaskContract(ctx, taskstore.CreateInput{VaultID: "vault-alpha", WorkspaceRoot: `C:\repo`, BaselineCommit: strings.Repeat("a", 40), Goal: "decision task", AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"pass"}, Risk: task.RiskMedium, IdempotencyKey: "task-alpha"})
	if err != nil {
		t.Fatal(err)
	}
	return created.Task
}

func createDecisionTestQuestion(t *testing.T, store *Store, taskRecord task.Record) decisionstore.Result {
	t.Helper()
	created, err := store.CreateDecision(context.Background(), decisionstore.CreateInput{VaultID: "vault-alpha", TaskID: taskRecord.ID, Category: decision.CategoryBlocking, ExpectedRepositoryRevision: taskRecord.BaselineCommit, Question: "Proceed?", Reason: "Needs choice", RiskIfUnanswered: "Blocked", SafeDefault: decision.SafeDefault{Action: "wait"}, BlockingScopes: []string{"step.execute"}, Options: []decision.Option{{ID: "yes", Label: "Yes", Consequence: "Proceed"}, {ID: "no", Label: "No", Consequence: "Stop"}}, IdempotencyKey: "decision-alpha"})
	if err != nil {
		t.Fatal(err)
	}
	return created
}
