package memoryevaluation

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorycontext"
)

var ErrInvalidEvaluation = errors.New("invalid memory evaluation")

type Case struct {
	ID                 string
	Request            memorycontext.Request
	ExpectedMemoryIDs  []string
	ForbiddenMemoryIDs []string
}

type CaseResult struct {
	ID            string
	Selected      []string
	Missing       []string
	ForbiddenSeen []string
}

type Report struct {
	Cases          int
	TruePositive   int
	FalsePositive  int
	FalseNegative  int
	PrecisionBasis int
	RecallBasis    int
	Results        []CaseResult
}

type Service struct{ provider memorycontext.Provider }

func New(provider memorycontext.Provider) (*Service, error) {
	if provider == nil {
		return nil, ErrInvalidEvaluation
	}
	return &Service{provider: provider}, nil
}

func (s *Service) Evaluate(ctx context.Context, cases []Case) (Report, error) {
	if ctx == nil || len(cases) == 0 || len(cases) > 256 {
		return Report{}, ErrInvalidEvaluation
	}
	report := Report{Cases: len(cases), Results: make([]CaseResult, 0, len(cases))}
	seenCases := make(map[string]struct{}, len(cases))
	for _, candidate := range cases {
		if strings.TrimSpace(candidate.ID) == "" || len(candidate.ID) > 128 {
			return Report{}, ErrInvalidEvaluation
		}
		if _, exists := seenCases[candidate.ID]; exists {
			return Report{}, ErrInvalidEvaluation
		}
		seenCases[candidate.ID] = struct{}{}
		expected, forbidden, err := sets(candidate.ExpectedMemoryIDs, candidate.ForbiddenMemoryIDs)
		if err != nil || len(expected) == 0 {
			return Report{}, ErrInvalidEvaluation
		}
		assembled, err := s.provider.Assemble(ctx, candidate.Request)
		if err != nil {
			return Report{}, err
		}
		result := CaseResult{ID: candidate.ID, Selected: make([]string, 0, len(assembled.Items))}
		selected := make(map[string]struct{}, len(assembled.Items))
		for _, item := range assembled.Items {
			if strings.TrimSpace(item.MemoryID) == "" {
				return Report{}, ErrInvalidEvaluation
			}
			if _, duplicate := selected[item.MemoryID]; duplicate {
				return Report{}, ErrInvalidEvaluation
			}
			selected[item.MemoryID] = struct{}{}
			result.Selected = append(result.Selected, item.MemoryID)
			if _, wanted := expected[item.MemoryID]; wanted {
				report.TruePositive++
			} else {
				report.FalsePositive++
			}
			if _, blocked := forbidden[item.MemoryID]; blocked {
				result.ForbiddenSeen = append(result.ForbiddenSeen, item.MemoryID)
			}
		}
		for memoryID := range expected {
			if _, found := selected[memoryID]; !found {
				report.FalseNegative++
				result.Missing = append(result.Missing, memoryID)
			}
		}
		sort.Strings(result.Missing)
		sort.Strings(result.ForbiddenSeen)
		report.Results = append(report.Results, result)
	}
	if denominator := report.TruePositive + report.FalsePositive; denominator > 0 {
		report.PrecisionBasis = report.TruePositive * 10_000 / denominator
	}
	if denominator := report.TruePositive + report.FalseNegative; denominator > 0 {
		report.RecallBasis = report.TruePositive * 10_000 / denominator
	}
	return report, nil
}

func sets(expected, forbidden []string) (map[string]struct{}, map[string]struct{}, error) {
	left := make(map[string]struct{}, len(expected))
	right := make(map[string]struct{}, len(forbidden))
	for _, values := range []struct {
		items []string
		set   map[string]struct{}
	}{{expected, left}, {forbidden, right}} {
		for _, value := range values.items {
			if strings.TrimSpace(value) == "" || len(value) > 128 {
				return nil, nil, ErrInvalidEvaluation
			}
			if _, duplicate := values.set[value]; duplicate {
				return nil, nil, ErrInvalidEvaluation
			}
			values.set[value] = struct{}{}
		}
	}
	for value := range left {
		if _, overlap := right[value]; overlap {
			return nil, nil, ErrInvalidEvaluation
		}
	}
	return left, right, nil
}
