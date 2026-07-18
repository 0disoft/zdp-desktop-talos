package memoryprojection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspacemapping"
	"github.com/0disoft/zdp-desktop-talos/internal/security/redaction"
)

func TestCompileIsDeterministicAndStructurallyExcludesUnsafeRecords(t *testing.T) {
	t.Parallel()
	service, err := New(redaction.NewScanner())
	if err != nil {
		t.Fatal(err)
	}
	public := projectionRecord(t, "memory-b", memory.StateStable, event.SensitivityPublic)
	public.Statement = "Use <safe> output | every time."
	public.Applicability = memory.Applicability{GoalTerms: []string{"projection", "safe"}}
	private := projectionRecord(t, "memory-private", memory.StateApproved, event.SensitivityPrivate)
	candidate := projectionRecord(t, "memory-candidate", memory.StateCandidate, event.SensitivityPublic)
	first, err := service.Compile(context.Background(), []memory.Record{private, public, candidate})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Compile(context.Background(), []memory.Record{candidate, public, private})
	if err != nil {
		t.Fatal(err)
	}
	if first.BundleSHA256 != second.BundleSHA256 || first.Included != 1 || first.ExcludedSensitivity != 1 || first.ExcludedLifecycle != 1 || len(first.Files) != 3 {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	for index := range first.Files {
		if first.Files[index].Path != second.Files[index].Path || first.Files[index].Content != second.Files[index].Content || first.Files[index].SHA256 != second.Files[index].SHA256 {
			t.Fatalf("projection output changed with input order: first=%+v second=%+v", first.Files[index], second.Files[index])
		}
		for _, forbidden := range []string{private.ID, candidate.ID, public.VaultID, public.Scope.WorkspaceID, public.EvidenceEventIDs[0]} {
			if strings.Contains(first.Files[index].Content, forbidden) {
				t.Fatalf("%s leaked %q", first.Files[index].Path, forbidden)
			}
		}
	}
	markdown := fileContent(t, first, "memory/memories.md")
	if strings.Contains(markdown, "<safe>") || !strings.Contains(markdown, "&lt;safe&gt;") || !strings.Contains(markdown, `\|`) {
		t.Fatalf("markdown was not safely escaped:\n%s", markdown)
	}
}

func TestCompileFailsClosedOnSecretFinding(t *testing.T) {
	t.Parallel()
	service, err := New(redaction.NewScanner())
	if err != nil {
		t.Fatal(err)
	}
	record := projectionRecord(t, "memory-secret", memory.StateApproved, event.SensitivityPublic)
	record.Statement = "token=github_pat_abcdefghijklmnopqrstuvwxyz123456"
	result, err := service.Compile(context.Background(), []memory.Record{record})
	if !errors.Is(err, ErrSecretFindings) || len(result.Files) != 0 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func TestCompileEmptyProjectionHasStableFiles(t *testing.T) {
	t.Parallel()
	service, err := New(redaction.NewScanner())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Compile(context.Background(), nil)
	if err != nil || result.Included != 0 || len(result.Files) != 3 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if got := fileContent(t, result, "memory/memories.yaml"); !strings.Contains(got, "records: []") {
		t.Fatalf("yaml=%q", got)
	}
}

func TestProjectionContractFixturesStayAligned(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "..", "contracts")
	for _, name := range []string{"memory-projection-record.schema.json", "memory-projection-document.schema.json"} {
		content, err := os.ReadFile(filepath.Join(root, "jsonschema", "projection", "v1", name))
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if json.Unmarshal(content, &schema) != nil || schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" || schema["additionalProperties"] != false {
			t.Fatalf("invalid schema contract %s", name)
		}
	}
	type document struct {
		Schema  string            `json:"schema"`
		Records []projectedRecord `json:"records"`
	}
	valid, err := os.ReadFile(filepath.Join(root, "fixtures", "projection", "v1", "valid-memory-projection.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(valid))
	decoder.DisallowUnknownFields()
	var parsed document
	if err := decoder.Decode(&parsed); err != nil || parsed.Schema != Schema || len(parsed.Records) != 1 || parsed.Records[0].Schema != RecordSchema {
		t.Fatalf("valid fixture parsed=%+v error=%v", parsed, err)
	}
	invalid, err := os.ReadFile(filepath.Join(root, "fixtures", "projection", "v1", "invalid-memory-projection-private-field.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder = json.NewDecoder(bytes.NewReader(invalid))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document{}); err == nil {
		t.Fatal("private fixture field was accepted")
	}
}

func projectionRecord(t *testing.T, id string, state memory.State, sensitivity event.Sensitivity) memory.Record {
	t.Helper()
	created := time.Date(2026, 7, 18, 6, 0, 0, 0, time.UTC)
	reviewed := created.Add(time.Minute)
	updated := reviewed
	revision := 2
	if state == memory.StateCandidate {
		reviewed = time.Time{}
		updated = created
		revision = 1
	}
	record := memory.Record{
		ID: id, VaultID: "00000000-0000-7000-8000-000000000001", Kind: memory.KindProcedure, State: state,
		Scope:     memory.Scope{Kind: memory.ScopeWorkspace, WorkspaceID: workspacemapping.ID("00000000-0000-7000-8000-000000000001", strings.Repeat("f", 64)), SourceWorkspaceHash: strings.Repeat("f", 64)},
		Statement: "Run the projection check.", Rationale: "The user approved this public rule.",
		Applicability: memory.Applicability{GoalTerms: []string{"projection"}}, EvidenceEventIDs: []string{"event-private-pointer"},
		SourceActor: "user", Confidence: 100, Sensitivity: sensitivity, Revision: revision,
		CreatedAt: created, UpdatedAt: updated, ReviewedAt: reviewed, CreatedEventID: "event-created", LastEventID: "event-reviewed",
	}
	if err := record.Validate(); err != nil {
		t.Fatalf("invalid fixture: %v", err)
	}
	return record
}

func fileContent(t *testing.T, result Result, path string) string {
	t.Helper()
	for _, file := range result.Files {
		if file.Path == path {
			return file.Content
		}
	}
	t.Fatalf("projection file %s not found", path)
	return ""
}
