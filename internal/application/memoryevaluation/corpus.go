package memoryevaluation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspacemapping"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorycontext"
)

const (
	CorpusSchema   = "talos.memory-evaluation-corpus/1"
	MaxCorpusBytes = 1 << 20
)

var (
	ErrInvalidCorpus = errors.New("invalid memory evaluation corpus")
	ErrCorpusGate    = errors.New("memory evaluation corpus gate failed")
)

type CorpusGate struct {
	MinPrecisionBasis        int  `json:"min_precision_basis"`
	MinRecallBasis           int  `json:"min_recall_basis"`
	MaxForbiddenIntervention int  `json:"max_forbidden_interventions"`
	RequireAllCases          bool `json:"require_all_cases"`
}

type CorpusMemory struct {
	ID        string           `json:"id"`
	Kind      memory.Kind      `json:"kind"`
	State     memory.State     `json:"state"`
	Scope     memory.ScopeKind `json:"scope"`
	GoalTerms []string         `json:"goal_terms"`
	Statement string           `json:"statement"`
}

type CorpusCase struct {
	ID                 string   `json:"id"`
	Goal               string   `json:"goal"`
	AllowedPaths       []string `json:"allowed_paths"`
	MaxCandidates      int      `json:"max_candidates"`
	MaxItems           int      `json:"max_items"`
	MaxBytes           int      `json:"max_bytes"`
	ExpectedMemoryIDs  []string `json:"expected_memory_ids"`
	ForbiddenMemoryIDs []string `json:"forbidden_memory_ids"`
}

type Corpus struct {
	Schema   string         `json:"schema"`
	Gate     CorpusGate     `json:"gate"`
	Memories []CorpusMemory `json:"memories"`
	Cases    []CorpusCase   `json:"cases"`
}

func DecodeCorpus(reader io.Reader) (Corpus, error) {
	if reader == nil {
		return Corpus{}, ErrInvalidCorpus
	}
	payload, err := io.ReadAll(io.LimitReader(reader, MaxCorpusBytes+1))
	if err != nil || len(payload) > MaxCorpusBytes {
		return Corpus{}, ErrInvalidCorpus
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var corpus Corpus
	if err := decoder.Decode(&corpus); err != nil {
		return Corpus{}, ErrInvalidCorpus
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Corpus{}, ErrInvalidCorpus
	}
	if err := corpus.Validate(); err != nil {
		return Corpus{}, err
	}
	return corpus, nil
}

func (c Corpus) Validate() error {
	if c.Schema != CorpusSchema || len(c.Memories) == 0 || len(c.Memories) > 256 || len(c.Cases) == 0 || len(c.Cases) > 256 || c.Gate.MinPrecisionBasis < 0 || c.Gate.MinPrecisionBasis > 10_000 || c.Gate.MinRecallBasis < 0 || c.Gate.MinRecallBasis > 10_000 || c.Gate.MaxForbiddenIntervention < 0 {
		return ErrInvalidCorpus
	}
	memoryByID := make(map[string]CorpusMemory, len(c.Memories))
	for _, candidate := range c.Memories {
		if !validCorpusID(candidate.ID) || candidate.Kind.Validate() != nil || candidate.State.Validate() != nil || candidate.Scope != memory.ScopeVault && candidate.Scope != memory.ScopeWorkspace || strings.TrimSpace(candidate.Statement) == "" || len(candidate.Statement) > memory.MaxStatementLength {
			return ErrInvalidCorpus
		}
		if _, duplicate := memoryByID[candidate.ID]; duplicate {
			return ErrInvalidCorpus
		}
		normalized, err := (memory.Applicability{GoalTerms: candidate.GoalTerms}).Normalize()
		if err != nil || !sameStrings(normalized.GoalTerms, candidate.GoalTerms) {
			return ErrInvalidCorpus
		}
		memoryByID[candidate.ID] = candidate
	}
	seenCases := make(map[string]struct{}, len(c.Cases))
	for _, candidate := range c.Cases {
		if !validCorpusID(candidate.ID) || strings.TrimSpace(candidate.Goal) == "" || len(candidate.Goal) > 4096 || candidate.MaxCandidates < 1 || candidate.MaxCandidates > 200 || candidate.MaxItems < 1 || candidate.MaxItems > 32 || candidate.MaxCandidates < candidate.MaxItems || candidate.MaxBytes < 256 || candidate.MaxBytes > 256<<10 {
			return ErrInvalidCorpus
		}
		if _, duplicate := seenCases[candidate.ID]; duplicate {
			return ErrInvalidCorpus
		}
		seenCases[candidate.ID] = struct{}{}
		for _, path := range candidate.AllowedPaths {
			if strings.TrimSpace(path) == "" || len(path) > 4096 {
				return ErrInvalidCorpus
			}
		}
		expected, forbidden, err := sets(candidate.ExpectedMemoryIDs, candidate.ForbiddenMemoryIDs)
		if err != nil || len(expected)+len(forbidden) == 0 {
			return ErrInvalidCorpus
		}
		for memoryID := range expected {
			definition, exists := memoryByID[memoryID]
			if !exists || definition.State != memory.StateApproved && definition.State != memory.StateStable {
				return ErrInvalidCorpus
			}
		}
		for memoryID := range forbidden {
			if _, exists := memoryByID[memoryID]; !exists {
				return ErrInvalidCorpus
			}
		}
	}
	return nil
}

func (c Corpus) EvaluationCases(vaultID, workspaceID string) ([]Case, error) {
	if strings.TrimSpace(vaultID) == "" || !workspacemapping.ValidWorkspaceID(workspaceID) || c.Validate() != nil {
		return nil, ErrInvalidCorpus
	}
	cases := make([]Case, 0, len(c.Cases))
	for _, candidate := range c.Cases {
		cases = append(cases, Case{
			ID: candidate.ID,
			Request: memorycontext.Request{
				VaultID: vaultID, WorkspaceID: workspaceID, Goal: candidate.Goal,
				AllowedPaths: append([]string(nil), candidate.AllowedPaths...), MaxCandidates: candidate.MaxCandidates,
				MaxItems: candidate.MaxItems, MaxBytes: candidate.MaxBytes,
			},
			ExpectedMemoryIDs:  append([]string(nil), candidate.ExpectedMemoryIDs...),
			ForbiddenMemoryIDs: append([]string(nil), candidate.ForbiddenMemoryIDs...),
		})
	}
	return cases, nil
}

func (c Corpus) CheckGate(report Report) error {
	if c.Validate() != nil || report.Cases != len(c.Cases) || len(report.Results) != len(c.Cases) || report.PrecisionBasis < c.Gate.MinPrecisionBasis || report.RecallBasis < c.Gate.MinRecallBasis || report.ForbiddenInterventions > c.Gate.MaxForbiddenIntervention || c.Gate.RequireAllCases && report.FailedCases != 0 {
		return ErrCorpusGate
	}
	expectedIDs := make(map[string]struct{}, len(c.Cases))
	for _, candidate := range c.Cases {
		expectedIDs[candidate.ID] = struct{}{}
	}
	for _, result := range report.Results {
		if _, exists := expectedIDs[result.ID]; !exists {
			return ErrCorpusGate
		}
		delete(expectedIDs, result.ID)
	}
	if len(expectedIDs) != 0 {
		return ErrCorpusGate
	}
	return nil
}

func validCorpusID(value string) bool {
	return strings.TrimSpace(value) == value && value != "" && len(value) <= 128 && !strings.ContainsAny(value, "\x00\r\n\t ")
}

func sameStrings(left, right []string) bool {
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
