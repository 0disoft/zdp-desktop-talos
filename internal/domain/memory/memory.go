package memory

import (
	"errors"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
)

const (
	SchemaVersion             = 1
	MaxStatementLength        = 4096
	MaxRationaleLength        = 2048
	MaxApplicabilityTerms     = 16
	MaxApplicabilityTermBytes = 128
	MaxEvidenceEvents         = 32
	MaxSourceActorLength      = 64
)

var (
	ErrInvalidRecord     = errors.New("invalid memory record")
	ErrInvalidTransition = errors.New("invalid memory state transition")
	termPattern          = regexp.MustCompile(`^[\pL\pN][\pL\pN _./:-]*$`)
)

type Kind string

const (
	KindPreference      Kind = "preference"
	KindConstraint      Kind = "constraint"
	KindDecision        Kind = "decision"
	KindProcedure       Kind = "procedure"
	KindFailurePattern  Kind = "failure_pattern"
	KindEnvironmentFact Kind = "environment_fact"
)

func (k Kind) Validate() error {
	switch k {
	case KindPreference, KindConstraint, KindDecision, KindProcedure, KindFailurePattern, KindEnvironmentFact:
		return nil
	default:
		return ErrInvalidRecord
	}
}

type State string

const (
	StateCandidate   State = "candidate"
	StateApproved    State = "approved"
	StateStable      State = "stable"
	StateStale       State = "stale"
	StateRejected    State = "rejected"
	StateQuarantined State = "quarantined"
	StateSuperseded  State = "superseded"
	StateDeprecated  State = "deprecated"
)

func (s State) Validate() error {
	switch s {
	case StateCandidate, StateApproved, StateStable, StateStale, StateRejected, StateQuarantined, StateSuperseded, StateDeprecated:
		return nil
	default:
		return ErrInvalidRecord
	}
}

func CanTransition(from, to State) bool {
	switch from {
	case StateCandidate:
		return to == StateApproved || to == StateRejected || to == StateQuarantined
	case StateApproved:
		return to == StateStable || to == StateStale || to == StateDeprecated || to == StateSuperseded
	case StateStable:
		return to == StateStale || to == StateDeprecated || to == StateSuperseded
	case StateStale:
		return to == StateApproved || to == StateDeprecated || to == StateSuperseded
	default:
		return false
	}
}

type ScopeKind string

const (
	ScopeVault     ScopeKind = "vault"
	ScopeWorkspace ScopeKind = "workspace"
)

type Scope struct {
	Kind          ScopeKind `json:"kind"`
	WorkspaceRoot string    `json:"workspace_root,omitempty"`
}

func (s Scope) Validate() error {
	switch s.Kind {
	case ScopeVault:
		if s.WorkspaceRoot != "" {
			return ErrInvalidRecord
		}
	case ScopeWorkspace:
		if strings.TrimSpace(s.WorkspaceRoot) == "" || len(s.WorkspaceRoot) > 4096 || !filepath.IsAbs(s.WorkspaceRoot) || strings.IndexByte(s.WorkspaceRoot, 0) >= 0 {
			return ErrInvalidRecord
		}
	default:
		return ErrInvalidRecord
	}
	return nil
}

type Applicability struct {
	GoalTerms []string `json:"goal_terms,omitempty"`
}

func (a Applicability) Normalize() (Applicability, error) {
	if len(a.GoalTerms) > MaxApplicabilityTerms {
		return Applicability{}, ErrInvalidRecord
	}
	seen := make(map[string]struct{}, len(a.GoalTerms))
	terms := make([]string, 0, len(a.GoalTerms))
	for _, raw := range a.GoalTerms {
		term := strings.ToLower(strings.TrimSpace(raw))
		if term == "" || len(term) > MaxApplicabilityTermBytes || !termPattern.MatchString(term) {
			return Applicability{}, ErrInvalidRecord
		}
		if _, exists := seen[term]; exists {
			continue
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	sort.Strings(terms)
	return Applicability{GoalTerms: terms}, nil
}

type Record struct {
	ID               string
	VaultID          string
	Kind             Kind
	State            State
	Scope            Scope
	Statement        string
	Rationale        string
	Applicability    Applicability
	EvidenceEventIDs []string
	SourceActor      string
	Confidence       int
	Sensitivity      event.Sensitivity
	Revision         int
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ReviewedAt       time.Time
	ExpiresAt        time.Time
	SupersededBy     string
	CreatedEventID   string
	LastEventID      string
}

func (r Record) Validate() error {
	if r.ID == "" || r.VaultID == "" || r.Kind.Validate() != nil || r.State.Validate() != nil || r.Scope.Validate() != nil || strings.TrimSpace(r.Statement) == "" || len(r.Statement) > MaxStatementLength || strings.TrimSpace(r.Rationale) == "" || len(r.Rationale) > MaxRationaleLength || len(r.EvidenceEventIDs) == 0 || len(r.EvidenceEventIDs) > MaxEvidenceEvents || strings.TrimSpace(r.SourceActor) == "" || len(r.SourceActor) > MaxSourceActorLength || r.Confidence < 0 || r.Confidence > 100 || r.Revision < 1 || r.CreatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) || r.CreatedEventID == "" || r.LastEventID == "" {
		return ErrInvalidRecord
	}
	if r.Sensitivity.Validate() != nil || r.Sensitivity == event.SensitivitySecret {
		return ErrInvalidRecord
	}
	applicability, err := r.Applicability.Normalize()
	if err != nil || !sameTerms(applicability.GoalTerms, r.Applicability.GoalTerms) {
		return ErrInvalidRecord
	}
	seenEvidence := make(map[string]struct{}, len(r.EvidenceEventIDs))
	for _, evidenceID := range r.EvidenceEventIDs {
		if strings.TrimSpace(evidenceID) == "" || len(evidenceID) > 128 {
			return ErrInvalidRecord
		}
		if _, exists := seenEvidence[evidenceID]; exists {
			return ErrInvalidRecord
		}
		seenEvidence[evidenceID] = struct{}{}
	}
	switch r.State {
	case StateCandidate:
		if !r.ReviewedAt.IsZero() {
			return ErrInvalidRecord
		}
	default:
		if r.ReviewedAt.IsZero() || r.ReviewedAt.Before(r.CreatedAt) || r.ReviewedAt.After(r.UpdatedAt) {
			return ErrInvalidRecord
		}
	}
	if !r.ExpiresAt.IsZero() && !r.ExpiresAt.After(r.CreatedAt) {
		return ErrInvalidRecord
	}
	if r.State == StateSuperseded {
		if strings.TrimSpace(r.SupersededBy) == "" || r.SupersededBy == r.ID || len(r.SupersededBy) > 128 {
			return ErrInvalidRecord
		}
	} else if r.SupersededBy != "" {
		return ErrInvalidRecord
	}
	return nil
}

func (r Record) IsExpired(at time.Time) bool {
	return !r.ExpiresAt.IsZero() && !at.Before(r.ExpiresAt)
}

func sameTerms(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
