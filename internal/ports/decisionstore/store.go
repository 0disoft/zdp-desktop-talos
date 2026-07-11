package decisionstore

import (
	"context"
	"errors"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/decision"
)

var (
	ErrInvalidCommand        = errors.New("invalid decision command")
	ErrNotFound              = errors.New("decision not found")
	ErrQuestionStale         = errors.New("decision question revision is stale")
	ErrRepositoryStale       = errors.New("decision repository revision is stale")
	ErrConflictRequired      = errors.New("decision has no conflict to resolve")
	ErrAnswerNotFound        = errors.New("decision answer not found")
	ErrIdempotencyConflict   = errors.New("decision idempotency conflict")
	ErrIdempotencyUnverified = errors.New("decision idempotency result cannot be verified")
)

type CreateInput struct {
	VaultID                    string
	TaskID                     string
	Category                   decision.Category
	ExpectedRepositoryRevision string
	Question                   string
	Reason                     string
	RiskIfUnanswered           string
	SafeDefault                decision.SafeDefault
	BlockingScopes             []string
	Options                    []decision.Option
	OccurredAt                 time.Time
	IdempotencyKey             string
}

type AnswerInput struct {
	VaultID                    string
	DecisionID                 string
	QuestionRevision           int
	ExpectedRepositoryRevision string
	SelectedOptionID           string
	Text                       string
	OccurredAt                 time.Time
	IdempotencyKey             string
}

type SupersedeInput struct {
	VaultID                    string
	DecisionID                 string
	ExpectedQuestionRevision   int
	ExpectedRepositoryRevision string
	Question                   string
	Reason                     string
	RiskIfUnanswered           string
	SafeDefault                decision.SafeDefault
	BlockingScopes             []string
	Options                    []decision.Option
	OccurredAt                 time.Time
	IdempotencyKey             string
}

type ResolveInput struct {
	VaultID                    string
	DecisionID                 string
	QuestionRevision           int
	ExpectedRepositoryRevision string
	SelectedAnswerID           string
	OccurredAt                 time.Time
	IdempotencyKey             string
}

type Result struct {
	Decision decision.Record
	Question decision.Question
	Answer   *decision.Answer
	Answers  []decision.Answer
}

type Store interface {
	CreateDecision(context.Context, CreateInput) (Result, error)
	GetDecision(context.Context, string) (Result, error)
	ListDecisions(context.Context, string, string, int) ([]Result, error)
	AnswerDecision(context.Context, AnswerInput) (Result, error)
	SupersedeDecision(context.Context, SupersedeInput) (Result, error)
	ResolveDecisionConflict(context.Context, ResolveInput) (Result, error)
}
