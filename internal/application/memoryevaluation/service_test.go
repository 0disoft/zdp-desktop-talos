package memoryevaluation

import (
	"context"
	"testing"

	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorycontext"
)

func TestEvaluateReportsRetrievalPrecisionRecallAndForbiddenIntervention(t *testing.T) {
	t.Parallel()
	service, err := New(&evaluationProvider{results: []memorycontext.Result{{Items: []memorycontext.Item{{MemoryID: "expected"}, {MemoryID: "noise"}, {MemoryID: "stale"}}}, {Items: []memorycontext.Item{{MemoryID: "second"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Evaluate(context.Background(), []Case{
		{ID: "case-1", Request: memorycontext.Request{VaultID: "vault"}, ExpectedMemoryIDs: []string{"expected", "missing"}, ForbiddenMemoryIDs: []string{"stale"}},
		{ID: "case-2", Request: memorycontext.Request{VaultID: "vault"}, ExpectedMemoryIDs: []string{"second"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.PassedCases != 1 || report.FailedCases != 1 || report.TruePositive != 2 || report.FalsePositive != 2 || report.FalseNegative != 1 || report.ForbiddenInterventions != 1 || report.PrecisionBasis != 5000 || report.RecallBasis != 6666 || len(report.Results[0].ForbiddenSeen) != 1 || len(report.Results[0].Missing) != 1 || len(report.Results[0].Unexpected) != 2 || report.Results[0].Passed || !report.Results[1].Passed {
		t.Fatalf("report=%+v", report)
	}
}

func TestEvaluateAcceptsNegativeOnlyCase(t *testing.T) {
	t.Parallel()
	service, err := New(&evaluationProvider{results: []memorycontext.Result{{}}})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Evaluate(context.Background(), []Case{{ID: "no-memory", Request: memorycontext.Request{VaultID: "vault"}, ForbiddenMemoryIDs: []string{"broad-noise"}}})
	if err != nil {
		t.Fatal(err)
	}
	if report.PassedCases != 1 || report.FailedCases != 0 || !report.Results[0].Passed || report.PrecisionBasis != 0 || report.RecallBasis != 0 {
		t.Fatalf("report=%+v", report)
	}
}

type evaluationProvider struct {
	results []memorycontext.Result
	index   int
}

func (p *evaluationProvider) Assemble(context.Context, memorycontext.Request) (memorycontext.Result, error) {
	result := p.results[p.index]
	p.index++
	return result, nil
}
