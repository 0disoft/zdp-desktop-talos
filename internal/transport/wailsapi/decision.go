package wailsapi

import (
	"context"
	"strings"

	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/decision"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/decisionstore"
)

type DecisionOptionDTO struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Consequence string `json:"consequence"`
}
type DecisionSafeDefaultDTO struct {
	Action            string   `json:"action"`
	ContinuableScopes []string `json:"continuable_scopes"`
}
type DecisionAnswerDTO struct {
	AnswerID         string `json:"answer_id"`
	SelectedOptionID string `json:"selected_option_id,omitempty"`
	Text             string `json:"text,omitempty"`
	CreatedAt        string `json:"created_at"`
}
type DecisionDTO struct {
	DecisionID                 string                 `json:"decision_id"`
	TaskID                     string                 `json:"task_id"`
	QuestionRevision           int                    `json:"question_revision"`
	Category                   string                 `json:"category"`
	State                      string                 `json:"state"`
	ExpectedRepositoryRevision string                 `json:"expected_repository_revision"`
	Question                   string                 `json:"question"`
	Reason                     string                 `json:"reason"`
	RiskIfUnanswered           string                 `json:"risk_if_unanswered"`
	SafeDefault                DecisionSafeDefaultDTO `json:"safe_default"`
	BlockingScopes             []string               `json:"blocking_scopes"`
	Options                    []DecisionOptionDTO    `json:"options"`
	Answer                     *DecisionAnswerDTO     `json:"answer,omitempty"`
	Answers                    []DecisionAnswerDTO    `json:"answers"`
	CreatedAt                  string                 `json:"created_at"`
	UpdatedAt                  string                 `json:"updated_at"`
}
type DecisionResult struct {
	Decision *DecisionDTO `json:"decision,omitempty"`
	Error    *TalosError  `json:"error,omitempty"`
}
type DecisionListResult struct {
	Decisions []DecisionDTO `json:"decisions"`
	Error     *TalosError   `json:"error,omitempty"`
}

type DecisionCreateRequest struct {
	TaskID           string                 `json:"task_id"`
	Category         string                 `json:"category"`
	Question         string                 `json:"question"`
	Reason           string                 `json:"reason"`
	RiskIfUnanswered string                 `json:"risk_if_unanswered"`
	SafeDefault      DecisionSafeDefaultDTO `json:"safe_default"`
	BlockingScopes   []string               `json:"blocking_scopes"`
	Options          []DecisionOptionDTO    `json:"options"`
	RequestID        string                 `json:"request_id"`
	CorrelationID    string                 `json:"correlation_id"`
}
type DecisionAnswerRequest struct {
	DecisionID       string `json:"decision_id"`
	QuestionRevision int    `json:"question_revision"`
	SelectedOptionID string `json:"selected_option_id"`
	Text             string `json:"text"`
	RequestID        string `json:"request_id"`
	CorrelationID    string `json:"correlation_id"`
}
type DecisionSupersedeRequest struct {
	DecisionID               string                 `json:"decision_id"`
	ExpectedQuestionRevision int                    `json:"expected_question_revision"`
	Question                 string                 `json:"question"`
	Reason                   string                 `json:"reason"`
	RiskIfUnanswered         string                 `json:"risk_if_unanswered"`
	SafeDefault              DecisionSafeDefaultDTO `json:"safe_default"`
	BlockingScopes           []string               `json:"blocking_scopes"`
	Options                  []DecisionOptionDTO    `json:"options"`
	RequestID                string                 `json:"request_id"`
	CorrelationID            string                 `json:"correlation_id"`
}
type DecisionResolveRequest struct {
	DecisionID       string `json:"decision_id"`
	QuestionRevision int    `json:"question_revision"`
	SelectedAnswerID string `json:"selected_answer_id"`
	RequestID        string `json:"request_id"`
	CorrelationID    string `json:"correlation_id"`
}

type DecisionService struct {
	vault     *VaultService
	workspace *WorkspaceService
}

func NewDecisionService(vault *VaultService, workspace *WorkspaceService) *DecisionService {
	return &DecisionService{vault: vault, workspace: workspace}
}

func (s *DecisionService) Create(request DecisionCreateRequest) DecisionResult {
	correlationID := normalizeCorrelationID(request.CorrelationID)
	if s.vault == nil || s.workspace == nil || strings.TrimSpace(request.TaskID) == "" || strings.TrimSpace(request.RequestID) == "" {
		return decisionError(decisionstore.ErrInvalidCommand, correlationID)
	}
	snapshot, err := s.workspace.decisionSnapshot()
	if err != nil {
		return decisionError(err, correlationID)
	}
	options := make([]decision.Option, 0, len(request.Options))
	for _, option := range request.Options {
		options = append(options, decision.Option{ID: option.ID, Label: option.Label, Consequence: option.Consequence})
	}
	result, err := s.vault.createDecision(vaultbootstrap.CreateDecisionInput{TaskID: strings.TrimSpace(request.TaskID), WorkspaceRoot: snapshot.Root, ExpectedRepositoryRevision: snapshot.BaselineCommit, Category: decision.Category(request.Category), Question: request.Question, Reason: request.Reason, RiskIfUnanswered: request.RiskIfUnanswered, SafeDefault: decision.SafeDefault{Action: request.SafeDefault.Action, ContinuableScopes: request.SafeDefault.ContinuableScopes}, BlockingScopes: request.BlockingScopes, Options: options, IdempotencyKey: "decision-create:" + strings.TrimSpace(request.RequestID)})
	if err != nil {
		return decisionError(err, correlationID)
	}
	dto := decisionDTO(result)
	return DecisionResult{Decision: &dto}
}

func (s *DecisionService) List(taskID, correlationID string) DecisionListResult {
	correlationID = normalizeCorrelationID(correlationID)
	if s.vault == nil || strings.TrimSpace(taskID) == "" {
		mapped := MapError(decisionstore.ErrInvalidCommand, correlationID)
		return DecisionListResult{Error: &mapped}
	}
	results, err := s.vault.listDecisions(strings.TrimSpace(taskID), 256)
	if err != nil {
		mapped := MapError(err, correlationID)
		return DecisionListResult{Error: &mapped}
	}
	dtos := make([]DecisionDTO, 0, len(results))
	for _, result := range results {
		dtos = append(dtos, decisionDTO(result))
	}
	return DecisionListResult{Decisions: dtos}
}

func (s *DecisionService) Answer(request DecisionAnswerRequest) DecisionResult {
	correlationID := normalizeCorrelationID(request.CorrelationID)
	if s.vault == nil || s.workspace == nil || strings.TrimSpace(request.DecisionID) == "" || request.QuestionRevision < 1 || strings.TrimSpace(request.RequestID) == "" {
		return decisionError(decisionstore.ErrInvalidCommand, correlationID)
	}
	snapshot, err := s.workspace.decisionSnapshot()
	if err != nil {
		return decisionError(err, correlationID)
	}
	result, err := s.vault.answerDecision(vaultbootstrap.AnswerDecisionInput{DecisionID: strings.TrimSpace(request.DecisionID), QuestionRevision: request.QuestionRevision, ExpectedRepositoryRevision: snapshot.BaselineCommit, SelectedOptionID: request.SelectedOptionID, Text: request.Text, IdempotencyKey: "decision-answer:" + strings.TrimSpace(request.RequestID)})
	if err != nil {
		return decisionError(err, correlationID)
	}
	dto := decisionDTO(result)
	return DecisionResult{Decision: &dto}
}

func (s *DecisionService) Supersede(request DecisionSupersedeRequest) DecisionResult {
	correlationID := normalizeCorrelationID(request.CorrelationID)
	if s.vault == nil || s.workspace == nil || strings.TrimSpace(request.DecisionID) == "" || request.ExpectedQuestionRevision < 1 || strings.TrimSpace(request.RequestID) == "" {
		return decisionError(decisionstore.ErrInvalidCommand, correlationID)
	}
	snapshot, err := s.workspace.decisionSnapshot()
	if err != nil {
		return decisionError(err, correlationID)
	}
	options := make([]decision.Option, 0, len(request.Options))
	for _, option := range request.Options {
		options = append(options, decision.Option{ID: option.ID, Label: option.Label, Consequence: option.Consequence})
	}
	result, err := s.vault.supersedeDecision(vaultbootstrap.SupersedeDecisionInput{DecisionID: strings.TrimSpace(request.DecisionID), ExpectedQuestionRevision: request.ExpectedQuestionRevision, ExpectedRepositoryRevision: snapshot.BaselineCommit, Question: request.Question, Reason: request.Reason, RiskIfUnanswered: request.RiskIfUnanswered, SafeDefault: decision.SafeDefault{Action: request.SafeDefault.Action, ContinuableScopes: request.SafeDefault.ContinuableScopes}, BlockingScopes: request.BlockingScopes, Options: options, IdempotencyKey: "decision-supersede:" + strings.TrimSpace(request.RequestID)})
	if err != nil {
		return decisionError(err, correlationID)
	}
	dto := decisionDTO(result)
	return DecisionResult{Decision: &dto}
}

func (s *DecisionService) ResolveConflict(request DecisionResolveRequest) DecisionResult {
	correlationID := normalizeCorrelationID(request.CorrelationID)
	if s.vault == nil || s.workspace == nil || strings.TrimSpace(request.DecisionID) == "" || request.QuestionRevision < 1 || strings.TrimSpace(request.SelectedAnswerID) == "" || strings.TrimSpace(request.RequestID) == "" {
		return decisionError(decisionstore.ErrInvalidCommand, correlationID)
	}
	snapshot, err := s.workspace.decisionSnapshot()
	if err != nil {
		return decisionError(err, correlationID)
	}
	result, err := s.vault.resolveDecisionConflict(vaultbootstrap.ResolveDecisionConflictInput{DecisionID: strings.TrimSpace(request.DecisionID), QuestionRevision: request.QuestionRevision, ExpectedRepositoryRevision: snapshot.BaselineCommit, SelectedAnswerID: strings.TrimSpace(request.SelectedAnswerID), IdempotencyKey: "decision-resolve:" + strings.TrimSpace(request.RequestID)})
	if err != nil {
		return decisionError(err, correlationID)
	}
	dto := decisionDTO(result)
	return DecisionResult{Decision: &dto}
}

func decisionError(err error, correlationID string) DecisionResult {
	mapped := MapError(err, correlationID)
	return DecisionResult{Error: &mapped}
}

func decisionDTO(result decisionstore.Result) DecisionDTO {
	options := make([]DecisionOptionDTO, 0, len(result.Question.Options))
	for _, option := range result.Question.Options {
		options = append(options, DecisionOptionDTO{ID: option.ID, Label: option.Label, Consequence: option.Consequence})
	}
	dto := DecisionDTO{DecisionID: result.Decision.ID, TaskID: result.Decision.TaskID, QuestionRevision: result.Decision.QuestionRevision, Category: string(result.Decision.Category), State: string(result.Decision.State), ExpectedRepositoryRevision: result.Decision.ExpectedRepositoryRevision, Question: result.Question.Question, Reason: result.Question.Reason, RiskIfUnanswered: result.Question.RiskIfUnanswered, SafeDefault: DecisionSafeDefaultDTO{Action: result.Question.SafeDefault.Action, ContinuableScopes: append([]string(nil), result.Question.SafeDefault.ContinuableScopes...)}, BlockingScopes: append([]string(nil), result.Question.BlockingScopes...), Options: options, CreatedAt: result.Decision.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), UpdatedAt: result.Decision.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")}
	dto.Answers = make([]DecisionAnswerDTO, 0, len(result.Answers))
	for _, answer := range result.Answers {
		dto.Answers = append(dto.Answers, DecisionAnswerDTO{AnswerID: answer.ID, SelectedOptionID: answer.SelectedOptionID, Text: answer.Text, CreatedAt: answer.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")})
	}
	if result.Answer != nil {
		dto.Answer = &DecisionAnswerDTO{AnswerID: result.Answer.ID, SelectedOptionID: result.Answer.SelectedOptionID, Text: result.Answer.Text, CreatedAt: result.Answer.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")}
	}
	return dto
}

func (s *VaultService) createDecision(input vaultbootstrap.CreateDecisionInput) (decisionstore.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		return decisionstore.Result{}, vaultbootstrap.ErrNotOpen
	}
	return s.session.CreateDecision(context.Background(), input)
}
func (s *VaultService) listDecisions(taskID string, limit int) ([]decisionstore.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		return nil, vaultbootstrap.ErrNotOpen
	}
	return s.session.ListDecisions(context.Background(), taskID, limit)
}
func (s *VaultService) answerDecision(input vaultbootstrap.AnswerDecisionInput) (decisionstore.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		return decisionstore.Result{}, vaultbootstrap.ErrNotOpen
	}
	return s.session.AnswerDecision(context.Background(), input)
}
func (s *VaultService) supersedeDecision(input vaultbootstrap.SupersedeDecisionInput) (decisionstore.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		return decisionstore.Result{}, vaultbootstrap.ErrNotOpen
	}
	return s.session.SupersedeDecision(context.Background(), input)
}
func (s *VaultService) resolveDecisionConflict(input vaultbootstrap.ResolveDecisionConflictInput) (decisionstore.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		return decisionstore.Result{}, vaultbootstrap.ErrNotOpen
	}
	return s.session.ResolveDecisionConflict(context.Background(), input)
}
