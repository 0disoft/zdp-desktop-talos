package memoryevaluation

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/application/contextassembly"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspacemapping"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorystore"
)

func TestTalosDevelopmentCorpusPassesContextAssemblyRegressionGate(t *testing.T) {
	corpus := loadDevelopmentCorpus(t)
	vaultID := "memory-evaluation-vault"
	sourceHash := strings.Repeat("e", 64)
	workspaceID := workspacemapping.ID(vaultID, sourceHash)
	provider, err := contextassembly.New(&corpusMemoryReader{records: corpusRecords(t, corpus, vaultID, workspaceID, sourceHash)})
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(provider)
	if err != nil {
		t.Fatal(err)
	}
	cases, err := corpus.EvaluationCases(vaultID, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Evaluate(context.Background(), cases)
	if err != nil {
		t.Fatal(err)
	}
	if err := corpus.CheckGate(report); err != nil {
		t.Fatalf("gate=%v report=%+v", err, report)
	}
	repeated, err := service.Evaluate(context.Background(), cases)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report, repeated) {
		t.Fatalf("evaluation is not deterministic:\nfirst=%+v\nsecond=%+v", report, repeated)
	}
	if report.Cases != 8 || report.PassedCases != 8 || report.FailedCases != 0 || report.TruePositive != 6 || report.FalsePositive != 0 || report.FalseNegative != 0 || report.ForbiddenInterventions != 0 || report.PrecisionBasis != 10_000 || report.RecallBasis != 10_000 {
		t.Fatalf("report=%+v", report)
	}
}

func TestCorpusValidationRejectsDriftAndGateFailure(t *testing.T) {
	corpus := loadDevelopmentCorpus(t)
	dangling := corpus
	dangling.Cases = append([]CorpusCase(nil), corpus.Cases...)
	dangling.Cases[0].ExpectedMemoryIDs = []string{"missing-memory"}
	if !errors.Is(dangling.Validate(), ErrInvalidCorpus) {
		t.Fatal("dangling expected memory was accepted")
	}
	overlap := corpus
	overlap.Cases = append([]CorpusCase(nil), corpus.Cases...)
	overlap.Cases[0].ForbiddenMemoryIDs = []string{"decision-revision-required"}
	if !errors.Is(overlap.Validate(), ErrInvalidCorpus) {
		t.Fatal("overlapping expected and forbidden memory was accepted")
	}
	if !errors.Is(corpus.CheckGate(Report{Cases: len(corpus.Cases), Results: make([]CaseResult, len(corpus.Cases)), PrecisionBasis: 9999, RecallBasis: 10_000}), ErrCorpusGate) {
		t.Fatal("below-threshold report passed the corpus gate")
	}
}

func TestDecodeCorpusRejectsUnknownAndTrailingFields(t *testing.T) {
	payload, err := os.ReadFile("testdata/talos-development-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	unknown := strings.Replace(string(payload), "\"gate\": {", "\"unknown\": true, \"gate\": {", 1)
	if _, err := DecodeCorpus(strings.NewReader(unknown)); !errors.Is(err, ErrInvalidCorpus) {
		t.Fatalf("unknown field error=%v", err)
	}
	if _, err := DecodeCorpus(strings.NewReader(string(payload) + "{}")); !errors.Is(err, ErrInvalidCorpus) {
		t.Fatalf("trailing object error=%v", err)
	}
	oversized := string(payload) + strings.Repeat(" ", MaxCorpusBytes-len(payload)+1)
	if _, err := DecodeCorpus(strings.NewReader(oversized)); !errors.Is(err, ErrInvalidCorpus) {
		t.Fatalf("oversized corpus error=%v", err)
	}
}

func loadDevelopmentCorpus(t *testing.T) Corpus {
	t.Helper()
	file, err := os.Open("testdata/talos-development-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	corpus, err := DecodeCorpus(file)
	if err != nil {
		t.Fatal(err)
	}
	return corpus
}

func corpusRecords(t *testing.T, corpus Corpus, vaultID, workspaceID, sourceHash string) []memory.Record {
	t.Helper()
	now := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	records := make([]memory.Record, 0, len(corpus.Memories))
	for _, candidate := range corpus.Memories {
		scope := memory.Scope{Kind: candidate.Scope}
		if candidate.Scope == memory.ScopeWorkspace {
			scope.WorkspaceID = workspaceID
			scope.SourceWorkspaceHash = sourceHash
		}
		record := memory.Record{
			ID: candidate.ID, VaultID: vaultID, Kind: candidate.Kind, State: candidate.State, Scope: scope,
			Statement: candidate.Statement, Rationale: "synthetic deterministic regression fixture",
			Applicability:    memory.Applicability{GoalTerms: append([]string(nil), candidate.GoalTerms...)},
			EvidenceEventIDs: []string{"fixture:" + candidate.ID}, SourceActor: "memory-evaluation-corpus",
			Confidence: 100, Sensitivity: event.SensitivityPrivate, Revision: 1,
			CreatedAt: now, UpdatedAt: now, ReviewedAt: now, CreatedEventID: "created:" + candidate.ID, LastEventID: "last:" + candidate.ID,
		}
		if err := record.Validate(); err != nil {
			t.Fatalf("memory %s: %v", candidate.ID, err)
		}
		records = append(records, record)
	}
	return records
}

type corpusMemoryReader struct{ records []memory.Record }

func (*corpusMemoryReader) GetMemory(context.Context, string, string) (memory.Record, error) {
	return memory.Record{}, memorystore.ErrNotFound
}
func (*corpusMemoryReader) ListMemoryCandidates(context.Context, string, int) ([]memory.Record, error) {
	return nil, nil
}
func (r *corpusMemoryReader) ListActiveMemories(_ context.Context, input memorystore.ListActiveInput) ([]memory.Record, error) {
	filtered := make([]memory.Record, 0, len(r.records))
	for _, record := range r.records {
		if record.VaultID != input.VaultID || record.State != memory.StateApproved && record.State != memory.StateStable || record.IsExpired(input.At) {
			continue
		}
		if record.Scope.Kind == memory.ScopeWorkspace && record.Scope.WorkspaceID != input.WorkspaceID {
			continue
		}
		filtered = append(filtered, record)
	}
	sort.Slice(filtered, func(left, right int) bool {
		if filtered[left].State != filtered[right].State {
			return filtered[left].State == memory.StateStable
		}
		if filtered[left].Confidence != filtered[right].Confidence {
			return filtered[left].Confidence > filtered[right].Confidence
		}
		if !filtered[left].UpdatedAt.Equal(filtered[right].UpdatedAt) {
			return filtered[left].UpdatedAt.After(filtered[right].UpdatedAt)
		}
		return filtered[left].ID < filtered[right].ID
	})
	if len(filtered) > input.Limit {
		filtered = filtered[:input.Limit]
	}
	return filtered, nil
}
func (r *corpusMemoryReader) ListMemories(context.Context, memorystore.ListInput) ([]memory.Record, error) {
	return append([]memory.Record(nil), r.records...), nil
}
