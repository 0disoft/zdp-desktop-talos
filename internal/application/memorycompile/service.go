package memorycompile

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/decision"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/decisionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorystore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/secretscanner"
)

const (
	decisionSourceActor = "memory-compiler:decision-v1"
	maxCompileDecisions = 256
	maxDerivedGoalTerms = 8
)

var (
	ErrInvalidRequest  = errors.New("invalid memory compilation request")
	ErrInvalidSource   = errors.New("memory compilation source is invalid")
	ErrSensitiveSource = errors.New("memory compilation source contains secret material")
	goalTokenPattern   = regexp.MustCompile(`[\pL\pN][\pL\pN_.:/-]{1,127}`)
)

type TaskReader interface {
	GetTask(context.Context, string) (task.Record, error)
	GetTaskContract(context.Context, string, int) (task.ContractRevision, error)
}

type DecisionReader interface {
	ListDecisions(context.Context, string, string, int) ([]decisionstore.Result, error)
}

type MemorySink interface {
	CreateMemoryCandidate(context.Context, memorystore.CreateCandidateInput) (memory.Record, error)
	GetMemory(context.Context, string, string) (memory.Record, error)
}

type Result struct {
	Eligible int
	Memories []memory.Record
}

type Service struct {
	tasks     TaskReader
	decisions DecisionReader
	memories  MemorySink
	scanner   secretscanner.Scanner
}

func New(tasks TaskReader, decisions DecisionReader, memories MemorySink, scanner secretscanner.Scanner) (*Service, error) {
	if tasks == nil || decisions == nil || memories == nil || scanner == nil {
		return nil, ErrInvalidRequest
	}
	return &Service{tasks: tasks, decisions: decisions, memories: memories, scanner: scanner}, nil
}

func (s *Service) CompileTask(ctx context.Context, vaultID, taskID string) (Result, error) {
	if ctx == nil || strings.TrimSpace(vaultID) == "" || strings.TrimSpace(taskID) == "" {
		return Result{}, ErrInvalidRequest
	}
	record, err := s.tasks.GetTask(ctx, taskID)
	if err != nil {
		return Result{}, err
	}
	if record.Validate() != nil || record.VaultID != vaultID {
		return Result{}, ErrInvalidSource
	}
	contract, err := s.tasks.GetTaskContract(ctx, taskID, record.CurrentRevision)
	if err != nil {
		return Result{}, err
	}
	if contract.Validate() != nil || contract.TaskID != taskID || contract.Revision != record.CurrentRevision || contract.BaselineCommit != record.BaselineCommit {
		return Result{}, ErrInvalidSource
	}
	results, err := s.decisions.ListDecisions(ctx, vaultID, taskID, maxCompileDecisions)
	if err != nil {
		return Result{}, err
	}
	inputs := make([]memorystore.CreateCandidateInput, 0, len(results))
	for _, source := range results {
		if source.Decision.State != decision.StateAnswered || source.Answer == nil {
			continue
		}
		input, err := candidateInput(record, contract, source)
		if err != nil {
			return Result{}, err
		}
		scan, err := s.scanner.Redact(ctx, input.Statement+"\n"+input.Rationale)
		if err != nil {
			return Result{}, err
		}
		if scan.Findings > 0 {
			return Result{}, ErrSensitiveSource
		}
		inputs = append(inputs, input)
	}
	output := Result{Eligible: len(inputs), Memories: make([]memory.Record, 0, len(inputs))}
	for _, input := range inputs {
		created, err := s.memories.CreateMemoryCandidate(ctx, input)
		if err != nil {
			return Result{}, err
		}
		current, err := s.memories.GetMemory(ctx, vaultID, created.ID)
		if err != nil {
			return Result{}, err
		}
		output.Memories = append(output.Memories, current)
	}
	return output, nil
}

func candidateInput(taskRecord task.Record, contract task.ContractRevision, source decisionstore.Result) (memorystore.CreateCandidateInput, error) {
	answer := source.Answer
	if answer == nil || source.Decision.Validate() != nil || source.Question.Validate(source.Decision.Category) != nil || answer.Validate() != nil || source.Decision.VaultID != taskRecord.VaultID || source.Decision.TaskID != taskRecord.ID || source.Decision.QuestionRevision != answer.QuestionRevision || source.Question.DecisionID != source.Decision.ID || source.Question.QuestionRevision != answer.QuestionRevision || answer.DecisionID != source.Decision.ID || source.Question.EventID == answer.EventID {
		return memorystore.CreateCandidateInput{}, ErrInvalidSource
	}
	answerText, err := selectedAnswer(source.Question, *answer)
	if err != nil {
		return memorystore.CreateCandidateInput{}, err
	}
	statement := truncateUTF8(fmt.Sprintf("Decision: %s Answer: %s", strings.TrimSpace(source.Question.Question), answerText), memory.MaxStatementLength)
	rationale := truncateUTF8("Derived from a user-confirmed task Decision. "+strings.TrimSpace(source.Question.Reason), memory.MaxRationaleLength)
	terms, err := memory.Applicability{GoalTerms: goalTerms(contract.Goal)}.Normalize()
	if err != nil {
		return memorystore.CreateCandidateInput{}, ErrInvalidSource
	}
	return memorystore.CreateCandidateInput{
		VaultID: taskRecord.VaultID,
		Kind:    memory.KindDecision,
		Scope: memory.Scope{
			Kind:                memory.ScopeWorkspace,
			WorkspaceID:         taskRecord.WorkspaceID,
			SourceWorkspaceHash: taskRecord.SourceWorkspaceHash,
		},
		Statement:        statement,
		Rationale:        rationale,
		Applicability:    terms,
		EvidenceEventIDs: []string{source.Question.EventID, answer.EventID},
		SourceActor:      decisionSourceActor,
		Confidence:       85,
		Sensitivity:      event.SensitivityPrivate,
		OccurredAt:       answer.CreatedAt,
		IdempotencyKey:   fmt.Sprintf("memory-decision:%s:%d:%s", source.Decision.ID, answer.QuestionRevision, answer.ID),
	}, nil
}

func selectedAnswer(question decision.Question, answer decision.Answer) (string, error) {
	if text := strings.TrimSpace(answer.Text); text != "" {
		return truncateUTF8(text, memory.MaxStatementLength/2), nil
	}
	for _, option := range question.Options {
		if option.ID != answer.SelectedOptionID {
			continue
		}
		selected := strings.TrimSpace(option.Label)
		if consequence := strings.TrimSpace(option.Consequence); consequence != "" {
			selected += " — " + consequence
		}
		return truncateUTF8(selected, memory.MaxStatementLength/2), nil
	}
	return "", ErrInvalidSource
}

func goalTerms(goal string) []string {
	seen := map[string]struct{}{}
	terms := make([]string, 0, maxDerivedGoalTerms)
	for _, token := range goalTokenPattern.FindAllString(strings.ToLower(goal), -1) {
		token = strings.Trim(token, "._:/-")
		if utf8.RuneCountInString(token) < 2 {
			continue
		}
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		terms = append(terms, token)
		if len(terms) == maxDerivedGoalTerms {
			break
		}
	}
	sort.Strings(terms)
	return terms
}

func truncateUTF8(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxBytes {
		return value
	}
	for maxBytes > 0 && !utf8.RuneStart(value[maxBytes]) {
		maxBytes--
	}
	return strings.TrimSpace(value[:maxBytes])
}
