package decision

import (
	"errors"
	"testing"
	"time"
)

func TestBlockingQuestionRequiresScopeAndDistinctOptions(t *testing.T) {
	t.Parallel()
	question := Question{DecisionID: "decision-1", QuestionRevision: 1, Question: "Publish API?", Reason: "Compatibility", RiskIfUnanswered: "Cannot undo", SafeDefault: SafeDefault{Action: "do not publish"}, Options: []Option{{ID: "yes", Label: "Yes", Consequence: "Public"}, {ID: "no", Label: "No", Consequence: "Private"}}, CreatedAt: time.Now(), EventID: "event-1"}
	if err := question.Validate(CategoryBlocking); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("missing scope error=%v", err)
	}
	question.BlockingScopes = []string{"api.publish"}
	if err := question.Validate(CategoryBlocking); err != nil {
		t.Fatal(err)
	}
	question.Options[1].ID = "yes"
	if err := question.Validate(CategoryBlocking); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("duplicate option error=%v", err)
	}
}

func TestAnswerRequiresExactlyOneValue(t *testing.T) {
	t.Parallel()
	answer := Answer{ID: "answer-1", DecisionID: "decision-1", QuestionRevision: 1, ExpectedRepositoryRevision: "0123456789012345678901234567890123456789", CreatedAt: time.Now(), EventID: "event-1"}
	if err := answer.Validate(); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("empty error=%v", err)
	}
	answer.SelectedOptionID = "yes"
	if err := answer.Validate(); err != nil {
		t.Fatal(err)
	}
	answer.Text = "also text"
	if err := answer.Validate(); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("both error=%v", err)
	}
}
