package sqliteevent

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/decision"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/id"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/decisionstore"
)

const (
	decisionCreatedEventType   = "decision.created"
	decisionAnsweredEventType  = "decision.answered"
	decisionEventSchemaVersion = 1
)

type decisionQuestionPayload struct {
	DecisionID       string               `json:"decision_id"`
	QuestionRevision int                  `json:"question_revision"`
	Question         string               `json:"question"`
	Reason           string               `json:"reason"`
	RiskIfUnanswered string               `json:"risk_if_unanswered"`
	SafeDefault      decision.SafeDefault `json:"safe_default"`
	BlockingScopes   []string             `json:"blocking_scopes"`
	Options          []decision.Option    `json:"options"`
	CreatedAt        string               `json:"created_at"`
}

type decisionAnswerPayload struct {
	AnswerID                   string         `json:"answer_id"`
	DecisionID                 string         `json:"decision_id"`
	QuestionRevision           int            `json:"question_revision"`
	ExpectedRepositoryRevision string         `json:"expected_repository_revision"`
	SelectedOptionID           string         `json:"selected_option_id,omitempty"`
	Text                       string         `json:"text,omitempty"`
	ResultingState             decision.State `json:"resulting_state"`
	CreatedAt                  string         `json:"created_at"`
}

type decisionPointer struct {
	id, vaultID, taskID, category, state, repositoryRevision, createdAt, updatedAt, questionEventID, lastEventID string
	questionRevision                                                                                             int
}

func (s *Store) CreateDecision(ctx context.Context, input decisionstore.CreateInput) (decisionstore.Result, error) {
	occurredAt := input.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = s.now()
	}
	occurredAt = occurredAt.UTC()
	validation := decision.Question{DecisionID: "validation-decision", QuestionRevision: 1, Question: input.Question, Reason: input.Reason, RiskIfUnanswered: input.RiskIfUnanswered, SafeDefault: input.SafeDefault, BlockingScopes: input.BlockingScopes, Options: input.Options, CreatedAt: occurredAt, EventID: "validation-event"}
	if input.VaultID == "" || input.TaskID == "" || input.ExpectedRepositoryRevision == "" || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 || validation.Validate(input.Category) != nil {
		return decisionstore.Result{}, decisionstore.ErrInvalidCommand
	}
	hash, err := decisionCreateHash(input)
	if err != nil {
		return decisionstore.Result{}, fmt.Errorf("hash decision command: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return decisionstore.Result{}, fmt.Errorf("begin decision transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existing, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, hash)
	if err != nil {
		return decisionstore.Result{}, mapDecisionIdempotencyError(err)
	}
	if found {
		return s.decisionResultFromEvent(ctx, tx, existing)
	}
	if err := requireTaskInVault(ctx, tx, input.TaskID, input.VaultID, input.ExpectedRepositoryRevision); err != nil {
		return decisionstore.Result{}, err
	}
	decisionID, err := id.UUIDv7(occurredAt, s.random)
	if err != nil {
		return decisionstore.Result{}, fmt.Errorf("generate decision id: %w", err)
	}
	payload := decisionQuestionPayload{DecisionID: decisionID, QuestionRevision: 1, Question: input.Question, Reason: input.Reason, RiskIfUnanswered: input.RiskIfUnanswered, SafeDefault: input.SafeDefault, BlockingScopes: append([]string(nil), input.BlockingScopes...), Options: append([]decision.Option(nil), input.Options...), CreatedAt: occurredAt.Format(time.RFC3339Nano)}
	record, err := s.decisionEvent(input.VaultID, decisionCreatedEventType, payload, occurredAt)
	if err != nil {
		return decisionstore.Result{}, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return decisionstore.Result{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO decisions(decision_id, vault_id, task_id, question_revision, category, state, expected_repository_revision, created_at, updated_at, question_event_id, last_event_id) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, decisionID, input.VaultID, input.TaskID, 1, string(input.Category), string(decision.StateOpen), input.ExpectedRepositoryRevision, payload.CreatedAt, payload.CreatedAt, record.ID, record.ID); err != nil {
		return decisionstore.Result{}, fmt.Errorf("insert decision: %w", err)
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, record.ID, hash); err != nil {
		return decisionstore.Result{}, err
	}
	if err := tx.Commit(); err != nil {
		return decisionstore.Result{}, fmt.Errorf("commit decision: %w", err)
	}
	return decisionResultFromQuestionPayload(input.VaultID, input.TaskID, input.Category, input.ExpectedRepositoryRevision, payload, record.ID)
}

func (s *Store) GetDecision(ctx context.Context, decisionID string) (decisionstore.Result, error) {
	if decisionID == "" {
		return decisionstore.Result{}, decisionstore.ErrInvalidCommand
	}
	pointer, err := scanDecisionPointer(s.db.QueryRowContext(ctx, decisionSelect+" WHERE decision_id = ?", decisionID))
	if err != nil {
		return decisionstore.Result{}, err
	}
	questionEvent, err := s.Get(ctx, pointer.questionEventID)
	if err != nil {
		return decisionstore.Result{}, err
	}
	result, err := decisionResultFromPointer(questionEvent, pointer)
	if err != nil {
		return decisionstore.Result{}, err
	}
	if pointer.lastEventID != pointer.questionEventID {
		answerEvent, err := s.Get(ctx, pointer.lastEventID)
		if err != nil {
			return decisionstore.Result{}, err
		}
		answer, _, err := answerFromEvent(answerEvent)
		if err != nil {
			return decisionstore.Result{}, err
		}
		result.Answer = &answer
	}
	return result, nil
}

func (s *Store) ListDecisions(ctx context.Context, vaultID, taskID string, limit int) ([]decisionstore.Result, error) {
	if vaultID == "" || taskID == "" || limit < 1 || limit > 256 {
		return nil, decisionstore.ErrInvalidCommand
	}
	rows, err := s.db.QueryContext(ctx, decisionSelect+" WHERE vault_id = ? AND task_id = ? ORDER BY created_at, decision_id LIMIT ?", vaultID, taskID, limit)
	if err != nil {
		return nil, fmt.Errorf("list decisions: %w", err)
	}
	defer rows.Close()
	pointers := make([]decisionPointer, 0)
	for rows.Next() {
		pointer, err := scanDecisionPointer(rows)
		if err != nil {
			return nil, err
		}
		pointers = append(pointers, pointer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate decisions: %w", err)
	}
	results := make([]decisionstore.Result, 0, len(pointers))
	for _, pointer := range pointers {
		questionEvent, err := s.Get(ctx, pointer.questionEventID)
		if err != nil {
			return nil, err
		}
		result, err := decisionResultFromPointer(questionEvent, pointer)
		if err != nil {
			return nil, err
		}
		if pointer.lastEventID != pointer.questionEventID {
			answerEvent, err := s.Get(ctx, pointer.lastEventID)
			if err != nil {
				return nil, err
			}
			answer, _, err := answerFromEvent(answerEvent)
			if err != nil {
				return nil, err
			}
			result.Answer = &answer
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *Store) AnswerDecision(ctx context.Context, input decisionstore.AnswerInput) (decisionstore.Result, error) {
	if input.VaultID == "" || input.DecisionID == "" || input.QuestionRevision < 1 || input.ExpectedRepositoryRevision == "" || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 {
		return decisionstore.Result{}, decisionstore.ErrInvalidCommand
	}
	occurredAt := input.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = s.now()
	}
	occurredAt = occurredAt.UTC()
	validation := decision.Answer{ID: "validation-answer", DecisionID: input.DecisionID, QuestionRevision: input.QuestionRevision, ExpectedRepositoryRevision: input.ExpectedRepositoryRevision, SelectedOptionID: input.SelectedOptionID, Text: strings.TrimSpace(input.Text), CreatedAt: occurredAt, EventID: "validation-event"}
	if validation.Validate() != nil {
		return decisionstore.Result{}, decisionstore.ErrInvalidCommand
	}
	commandHash, err := decisionAnswerCommandHash(input)
	if err != nil {
		return decisionstore.Result{}, fmt.Errorf("hash answer command: %w", err)
	}
	answerHashBytes, err := requestHash(struct{ SelectedOptionID, Text string }{input.SelectedOptionID, strings.TrimSpace(input.Text)})
	if err != nil {
		return decisionstore.Result{}, err
	}
	answerHash := hex.EncodeToString(answerHashBytes)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return decisionstore.Result{}, fmt.Errorf("begin answer transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	existingEvent, found, err := s.resolveIdempotency(ctx, tx, input.IdempotencyKey, commandHash)
	if err != nil {
		return decisionstore.Result{}, mapDecisionIdempotencyError(err)
	}
	if found {
		return s.decisionResultFromEvent(ctx, tx, existingEvent)
	}
	pointer, err := scanDecisionPointer(tx.QueryRowContext(ctx, decisionSelect+" WHERE decision_id = ? AND vault_id = ?", input.DecisionID, input.VaultID))
	if err != nil {
		return decisionstore.Result{}, err
	}
	if pointer.questionRevision != input.QuestionRevision {
		return decisionstore.Result{}, decisionstore.ErrQuestionStale
	}
	if pointer.repositoryRevision != input.ExpectedRepositoryRevision {
		return decisionstore.Result{}, decisionstore.ErrRepositoryStale
	}
	questionEvent, err := s.getEventInTx(tx, pointer.questionEventID)
	if err != nil {
		return decisionstore.Result{}, err
	}
	questionResult, err := decisionResultFromPointer(questionEvent, pointer)
	if err != nil {
		return decisionstore.Result{}, err
	}
	if input.SelectedOptionID != "" && !questionHasOption(questionResult.Question, input.SelectedOptionID) {
		return decisionstore.Result{}, decisionstore.ErrInvalidCommand
	}
	var existingAnswerEventID string
	if err := tx.QueryRowContext(ctx, `SELECT event_id FROM decision_answers WHERE decision_id = ? AND answer_hash = ?`, input.DecisionID, answerHash).Scan(&existingAnswerEventID); err == nil {
		record, err := s.getEventInTx(tx, existingAnswerEventID)
		if err != nil {
			return decisionstore.Result{}, err
		}
		answer, _, err := answerFromEvent(record)
		if err != nil {
			return decisionstore.Result{}, err
		}
		questionResult.Decision.State = decision.State(pointer.state)
		questionResult.Answer = &answer
		return questionResult, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return decisionstore.Result{}, fmt.Errorf("find equivalent answer: %w", err)
	}
	var answerCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM decision_answers WHERE decision_id = ?`, input.DecisionID).Scan(&answerCount); err != nil {
		return decisionstore.Result{}, fmt.Errorf("count decision answers: %w", err)
	}
	resultingState := decision.StateAnswered
	if answerCount > 0 {
		resultingState = decision.StateConflicted
	}
	answerID, err := id.UUIDv7(occurredAt, s.random)
	if err != nil {
		return decisionstore.Result{}, fmt.Errorf("generate answer id: %w", err)
	}
	payload := decisionAnswerPayload{AnswerID: answerID, DecisionID: input.DecisionID, QuestionRevision: input.QuestionRevision, ExpectedRepositoryRevision: input.ExpectedRepositoryRevision, SelectedOptionID: input.SelectedOptionID, Text: strings.TrimSpace(input.Text), ResultingState: resultingState, CreatedAt: occurredAt.Format(time.RFC3339Nano)}
	record, err := s.decisionEvent(input.VaultID, decisionAnsweredEventType, payload, occurredAt)
	if err != nil {
		return decisionstore.Result{}, err
	}
	if err := s.insertEvent(ctx, tx, record); err != nil {
		return decisionstore.Result{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO decision_answers(answer_id, decision_id, question_revision, expected_repository_revision, answer_hash, created_at, event_id) VALUES(?, ?, ?, ?, ?, ?, ?)`, answerID, input.DecisionID, input.QuestionRevision, input.ExpectedRepositoryRevision, answerHash, payload.CreatedAt, record.ID); err != nil {
		return decisionstore.Result{}, fmt.Errorf("insert decision answer: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE decisions SET state = ?, updated_at = ?, last_event_id = ? WHERE decision_id = ?`, string(resultingState), payload.CreatedAt, record.ID, input.DecisionID); err != nil {
		return decisionstore.Result{}, fmt.Errorf("update decision state: %w", err)
	}
	if err := claimIdempotency(ctx, tx, input.IdempotencyKey, record.ID, commandHash); err != nil {
		return decisionstore.Result{}, err
	}
	if err := tx.Commit(); err != nil {
		return decisionstore.Result{}, fmt.Errorf("commit decision answer: %w", err)
	}
	answer, _, err := answerFromEvent(record)
	if err != nil {
		return decisionstore.Result{}, err
	}
	questionResult.Decision.State = resultingState
	questionResult.Decision.UpdatedAt = occurredAt
	questionResult.Decision.LastEventID = record.ID
	questionResult.Answer = &answer
	return questionResult, nil
}

const decisionSelect = `SELECT decision_id, vault_id, task_id, question_revision, category, state, expected_repository_revision, created_at, updated_at, question_event_id, last_event_id FROM decisions`

func scanDecisionPointer(row scanner) (decisionPointer, error) {
	var p decisionPointer
	if err := row.Scan(&p.id, &p.vaultID, &p.taskID, &p.questionRevision, &p.category, &p.state, &p.repositoryRevision, &p.createdAt, &p.updatedAt, &p.questionEventID, &p.lastEventID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return decisionPointer{}, decisionstore.ErrNotFound
		}
		return decisionPointer{}, fmt.Errorf("scan decision pointer: %w", err)
	}
	return p, nil
}

func (s *Store) decisionResultFromEvent(ctx context.Context, tx *sql.Tx, record event.Record) (decisionstore.Result, error) {
	switch record.Type {
	case decisionCreatedEventType:
		var payload decisionQuestionPayload
		if err := json.Unmarshal(record.Payload, &payload); err != nil {
			return decisionstore.Result{}, err
		}
		pointer, err := scanDecisionPointer(tx.QueryRowContext(ctx, decisionSelect+" WHERE decision_id = ?", payload.DecisionID))
		if err != nil {
			return decisionstore.Result{}, err
		}
		result, err := decisionResultFromQuestionPayload(pointer.vaultID, pointer.taskID, decision.Category(pointer.category), pointer.repositoryRevision, payload, record.ID)
		if err != nil {
			return decisionstore.Result{}, err
		}
		result.Decision.State = decision.StateOpen
		return result, nil
	case decisionAnsweredEventType:
		answer, state, err := answerFromEvent(record)
		if err != nil {
			return decisionstore.Result{}, err
		}
		pointer, err := scanDecisionPointer(tx.QueryRowContext(ctx, decisionSelect+" WHERE decision_id = ?", answer.DecisionID))
		if err != nil {
			return decisionstore.Result{}, err
		}
		questionEvent, err := s.getEventInTx(tx, pointer.questionEventID)
		if err != nil {
			return decisionstore.Result{}, err
		}
		result, err := decisionResultFromPointer(questionEvent, pointer)
		if err != nil {
			return decisionstore.Result{}, err
		}
		result.Decision.State = state
		result.Decision.UpdatedAt = answer.CreatedAt
		result.Decision.LastEventID = record.ID
		result.Answer = &answer
		return result, nil
	default:
		return decisionstore.Result{}, decisionstore.ErrIdempotencyConflict
	}
}

func decisionResultFromPointer(record event.Record, pointer decisionPointer) (decisionstore.Result, error) {
	if record.Type != decisionCreatedEventType || record.ID != pointer.questionEventID {
		return decisionstore.Result{}, errors.New("decision question pointer is invalid")
	}
	var payload decisionQuestionPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return decisionstore.Result{}, fmt.Errorf("decode decision question: %w", err)
	}
	result, err := decisionResultFromQuestionPayload(pointer.vaultID, pointer.taskID, decision.Category(pointer.category), pointer.repositoryRevision, payload, record.ID)
	if err != nil {
		return decisionstore.Result{}, err
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, pointer.updatedAt)
	if err != nil {
		return decisionstore.Result{}, err
	}
	result.Decision.State = decision.State(pointer.state)
	result.Decision.UpdatedAt = updatedAt
	result.Decision.LastEventID = pointer.lastEventID
	return result, result.Decision.Validate()
}

func decisionResultFromQuestionPayload(vaultID, taskID string, category decision.Category, repositoryRevision string, payload decisionQuestionPayload, eventID string) (decisionstore.Result, error) {
	createdAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
	if err != nil {
		return decisionstore.Result{}, err
	}
	result := decisionstore.Result{Decision: decision.Record{ID: payload.DecisionID, VaultID: vaultID, TaskID: taskID, QuestionRevision: payload.QuestionRevision, Category: category, State: decision.StateOpen, ExpectedRepositoryRevision: repositoryRevision, CreatedAt: createdAt, UpdatedAt: createdAt, LastEventID: eventID}, Question: decision.Question{DecisionID: payload.DecisionID, QuestionRevision: payload.QuestionRevision, Question: payload.Question, Reason: payload.Reason, RiskIfUnanswered: payload.RiskIfUnanswered, SafeDefault: payload.SafeDefault, BlockingScopes: payload.BlockingScopes, Options: payload.Options, CreatedAt: createdAt, EventID: eventID}}
	if err := result.Decision.Validate(); err != nil {
		return decisionstore.Result{}, err
	}
	if err := result.Question.Validate(category); err != nil {
		return decisionstore.Result{}, err
	}
	return result, nil
}

func answerFromEvent(record event.Record) (decision.Answer, decision.State, error) {
	if record.Type != decisionAnsweredEventType {
		return decision.Answer{}, "", errors.New("decision answer event type is invalid")
	}
	var payload decisionAnswerPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return decision.Answer{}, "", err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
	if err != nil {
		return decision.Answer{}, "", err
	}
	answer := decision.Answer{ID: payload.AnswerID, DecisionID: payload.DecisionID, QuestionRevision: payload.QuestionRevision, ExpectedRepositoryRevision: payload.ExpectedRepositoryRevision, SelectedOptionID: payload.SelectedOptionID, Text: payload.Text, CreatedAt: createdAt, EventID: record.ID}
	if err := answer.Validate(); err != nil {
		return decision.Answer{}, "", err
	}
	if payload.ResultingState != decision.StateAnswered && payload.ResultingState != decision.StateConflicted {
		return decision.Answer{}, "", errors.New("decision answer state is invalid")
	}
	return answer, payload.ResultingState, nil
}

func (s *Store) decisionEvent(vaultID, eventType string, payload any, occurredAt time.Time) (event.Record, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return event.Record{}, fmt.Errorf("encode decision event: %w", err)
	}
	return s.newEventRecord(vaultID, eventType, decisionEventSchemaVersion, event.SensitivityPrivate, encoded, occurredAt)
}

func requireTaskInVault(ctx context.Context, tx *sql.Tx, taskID, vaultID, repositoryRevision string) error {
	var storedVaultID, baseline string
	if err := tx.QueryRowContext(ctx, `SELECT vault_id, baseline_commit FROM tasks WHERE task_id = ?`, taskID).Scan(&storedVaultID, &baseline); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return decisionstore.ErrInvalidCommand
		}
		return err
	}
	if storedVaultID != vaultID {
		return decisionstore.ErrInvalidCommand
	}
	if baseline != repositoryRevision {
		return decisionstore.ErrRepositoryStale
	}
	return nil
}

func questionHasOption(question decision.Question, optionID string) bool {
	for _, option := range question.Options {
		if option.ID == optionID {
			return true
		}
	}
	return false
}

func decisionCreateHash(input decisionstore.CreateInput) ([]byte, error) {
	input.OccurredAt = input.OccurredAt.UTC()
	return requestHash(input)
}
func decisionAnswerCommandHash(input decisionstore.AnswerInput) ([]byte, error) {
	input.OccurredAt = input.OccurredAt.UTC()
	input.Text = strings.TrimSpace(input.Text)
	return requestHash(input)
}
func mapDecisionIdempotencyError(err error) error {
	switch {
	case errors.Is(err, ErrIdempotencyConflict):
		return decisionstore.ErrIdempotencyConflict
	case errors.Is(err, ErrIdempotencyUnverifiable):
		return decisionstore.ErrIdempotencyUnverified
	default:
		return err
	}
}
