package decision

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	MaxTextLength = 4096
	MaxOptions    = 16
	MaxScopes     = 128
)

var (
	ErrInvalidRecord = errors.New("invalid decision record")
	commitPattern    = regexp.MustCompile(`^[0-9a-f]{40,64}$`)
)

type Category string

const (
	CategoryBlocking Category = "blocking"
	CategoryQuality  Category = "quality"
	CategoryFollowUp Category = "follow_up"
)

type State string

const (
	StateOpen       State = "open"
	StateAnswered   State = "answered"
	StateConflicted State = "conflicted"
)

type Option struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Consequence string `json:"consequence"`
}

type SafeDefault struct {
	Action            string   `json:"action"`
	ContinuableScopes []string `json:"continuable_scopes"`
}

type Record struct {
	ID                         string
	VaultID                    string
	TaskID                     string
	QuestionRevision           int
	Category                   Category
	State                      State
	ExpectedRepositoryRevision string
	CreatedAt                  time.Time
	UpdatedAt                  time.Time
	LastEventID                string
}

type Question struct {
	DecisionID       string
	QuestionRevision int
	Question         string
	Reason           string
	RiskIfUnanswered string
	SafeDefault      SafeDefault
	BlockingScopes   []string
	Options          []Option
	CreatedAt        time.Time
	EventID          string
}

type Answer struct {
	ID                         string
	DecisionID                 string
	QuestionRevision           int
	ExpectedRepositoryRevision string
	SelectedOptionID           string
	Text                       string
	CreatedAt                  time.Time
	EventID                    string
}

func (r Record) Validate() error {
	if r.ID == "" || r.VaultID == "" || r.TaskID == "" || r.QuestionRevision < 1 || !commitPattern.MatchString(r.ExpectedRepositoryRevision) || r.CreatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) || r.LastEventID == "" {
		return fmt.Errorf("%w: decision identity or provenance is invalid", ErrInvalidRecord)
	}
	if r.Category != CategoryBlocking && r.Category != CategoryQuality && r.Category != CategoryFollowUp {
		return fmt.Errorf("%w: category is invalid", ErrInvalidRecord)
	}
	if r.State != StateOpen && r.State != StateAnswered && r.State != StateConflicted {
		return fmt.Errorf("%w: state is invalid", ErrInvalidRecord)
	}
	return nil
}

func (q Question) Validate(category Category) error {
	if q.DecisionID == "" || q.QuestionRevision < 1 || !validText(q.Question) || !validText(q.Reason) || !validText(q.RiskIfUnanswered) || q.CreatedAt.IsZero() || q.EventID == "" {
		return fmt.Errorf("%w: question content or provenance is invalid", ErrInvalidRecord)
	}
	if !validText(q.SafeDefault.Action) || len(q.Options) < 2 || len(q.Options) > MaxOptions || len(q.BlockingScopes) > MaxScopes || len(q.SafeDefault.ContinuableScopes) > MaxScopes {
		return fmt.Errorf("%w: question choices or scopes are invalid", ErrInvalidRecord)
	}
	if category == CategoryBlocking && len(q.BlockingScopes) == 0 {
		return fmt.Errorf("%w: blocking decision requires a blocking scope", ErrInvalidRecord)
	}
	if category != CategoryBlocking && len(q.BlockingScopes) != 0 {
		return fmt.Errorf("%w: non-blocking decision cannot block scopes", ErrInvalidRecord)
	}
	if err := validateUnique(q.BlockingScopes); err != nil {
		return err
	}
	if err := validateUnique(q.SafeDefault.ContinuableScopes); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(q.Options))
	for _, option := range q.Options {
		if !validIdentifier(option.ID) || !validText(option.Label) || !validText(option.Consequence) {
			return fmt.Errorf("%w: option is invalid", ErrInvalidRecord)
		}
		if _, exists := seen[option.ID]; exists {
			return fmt.Errorf("%w: option id is duplicated", ErrInvalidRecord)
		}
		seen[option.ID] = struct{}{}
	}
	return nil
}

func (a Answer) Validate() error {
	if a.ID == "" || a.DecisionID == "" || a.QuestionRevision < 1 || !commitPattern.MatchString(a.ExpectedRepositoryRevision) || a.CreatedAt.IsZero() || a.EventID == "" {
		return fmt.Errorf("%w: answer identity or provenance is invalid", ErrInvalidRecord)
	}
	if (a.SelectedOptionID == "") == (strings.TrimSpace(a.Text) == "") || len(a.Text) > MaxTextLength {
		return fmt.Errorf("%w: answer must contain exactly one value", ErrInvalidRecord)
	}
	if a.SelectedOptionID != "" && !validIdentifier(a.SelectedOptionID) {
		return fmt.Errorf("%w: selected option is invalid", ErrInvalidRecord)
	}
	return nil
}

func validText(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= MaxTextLength && !strings.ContainsRune(value, 0)
}
func validIdentifier(value string) bool {
	return validText(value) && len(value) <= 128 && !strings.ContainsAny(value, "\r\n\t")
}
func validateUnique(values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !validIdentifier(value) {
			return fmt.Errorf("%w: scope is invalid", ErrInvalidRecord)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("%w: scope is duplicated", ErrInvalidRecord)
		}
		seen[value] = struct{}{}
	}
	return nil
}
