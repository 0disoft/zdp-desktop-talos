package memoryprojection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/secretscanner"
)

const (
	Schema             = "talos.memory-projection/1"
	RecordSchema       = "talos.memory-projection-record/1"
	MaxProjectionItems = 10_000
	MaxProjectionBytes = 64 << 20
)

var (
	ErrInvalidRequest = errors.New("invalid memory projection request")
	ErrSecretFindings = errors.New("memory projection contains secret findings")
	ErrOutputLimit    = errors.New("memory projection output limit exceeded")
)

type File struct {
	Path      string
	MediaType string
	Content   string
	SHA256    string
}

type Result struct {
	Schema                string
	Files                 []File
	BundleSHA256          string
	Included              int
	ExcludedSensitivity   int
	ExcludedLifecycle     int
	SecretFindingsBlocked int
}

type projectedRecord struct {
	Schema        string   `json:"schema"`
	MemoryID      string   `json:"memory_id"`
	Kind          string   `json:"kind"`
	State         string   `json:"state"`
	Scope         string   `json:"scope"`
	Statement     string   `json:"statement"`
	Rationale     string   `json:"rationale"`
	GoalTerms     []string `json:"goal_terms"`
	SourceActor   string   `json:"source_actor"`
	SourceRef     string   `json:"source_ref"`
	EvidenceCount int      `json:"evidence_count"`
	Confidence    int      `json:"confidence"`
	Revision      int      `json:"revision"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
	ReviewedAt    string   `json:"reviewed_at"`
	ExpiresAt     string   `json:"expires_at,omitempty"`
	SupersededBy  string   `json:"superseded_by,omitempty"`
}

type Service struct {
	scanner secretscanner.Scanner
}

func New(scanner secretscanner.Scanner) (*Service, error) {
	if scanner == nil {
		return nil, ErrInvalidRequest
	}
	return &Service{scanner: scanner}, nil
}

func (s *Service) Compile(ctx context.Context, records []memory.Record) (Result, error) {
	if ctx == nil || len(records) > MaxProjectionItems {
		return Result{}, ErrInvalidRequest
	}
	selected := make([]projectedRecord, 0, len(records))
	result := Result{Schema: Schema}
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if err := record.Validate(); err != nil {
			return Result{}, ErrInvalidRequest
		}
		if record.Sensitivity != event.SensitivityPublic {
			result.ExcludedSensitivity++
			continue
		}
		if !projectableState(record.State) {
			result.ExcludedLifecycle++
			continue
		}
		selected = append(selected, projectRecord(record))
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].MemoryID < selected[j].MemoryID })
	contents, err := render(selected)
	if err != nil {
		return Result{}, err
	}
	paths := []struct {
		path      string
		mediaType string
		content   string
	}{
		{"memory/memories.jsonl", "application/x-ndjson", contents.jsonl},
		{"memory/memories.md", "text/markdown; charset=utf-8", contents.markdown},
		{"memory/memories.yaml", "application/yaml", contents.yaml},
	}
	bundleHash := sha256.New()
	for _, candidate := range paths {
		if len(candidate.content) > MaxProjectionBytes {
			return Result{}, ErrOutputLimit
		}
		scanned, scanErr := s.scanner.Redact(ctx, candidate.content)
		if scanErr != nil {
			return Result{}, scanErr
		}
		if scanned.Findings > 0 || scanned.Text != candidate.content {
			result.SecretFindingsBlocked += scanned.Findings
			return Result{}, fmt.Errorf("%w: %d", ErrSecretFindings, result.SecretFindingsBlocked)
		}
		digest := sha256.Sum256([]byte(candidate.content))
		result.Files = append(result.Files, File{Path: candidate.path, MediaType: candidate.mediaType, Content: candidate.content, SHA256: hex.EncodeToString(digest[:])})
		_, _ = bundleHash.Write([]byte(candidate.path))
		_, _ = bundleHash.Write([]byte{0})
		_, _ = bundleHash.Write(digest[:])
	}
	result.Included = len(selected)
	result.BundleSHA256 = hex.EncodeToString(bundleHash.Sum(nil))
	return result, nil
}

func projectableState(state memory.State) bool {
	switch state {
	case memory.StateApproved, memory.StateStable, memory.StateStale, memory.StateDeprecated, memory.StateSuperseded:
		return true
	default:
		return false
	}
}

func projectRecord(record memory.Record) projectedRecord {
	result := projectedRecord{
		Schema: RecordSchema, MemoryID: record.ID, Kind: string(record.Kind), State: string(record.State), Scope: string(record.Scope.Kind),
		Statement: record.Statement, Rationale: record.Rationale, GoalTerms: append([]string(nil), record.Applicability.GoalTerms...),
		SourceActor: record.SourceActor, SourceRef: "event:" + record.CreatedEventID, EvidenceCount: len(record.EvidenceEventIDs),
		Confidence: record.Confidence, Revision: record.Revision, CreatedAt: formatTime(record.CreatedAt), UpdatedAt: formatTime(record.UpdatedAt), ReviewedAt: formatTime(record.ReviewedAt),
		SupersededBy: record.SupersededBy,
	}
	if !record.ExpiresAt.IsZero() {
		result.ExpiresAt = formatTime(record.ExpiresAt)
	}
	return result
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

type rendered struct{ jsonl, markdown, yaml string }

func render(records []projectedRecord) (rendered, error) {
	var jsonl, markdown, yaml strings.Builder
	markdown.WriteString("# Talos memory projection\n\n")
	markdown.WriteString("Schema: `" + Schema + "`\n\n")
	markdown.WriteString("| Memory | Kind | State | Scope | Statement | Revision | Updated |\n")
	markdown.WriteString("| --- | --- | --- | --- | --- | ---: | --- |\n")
	yaml.WriteString("schema: " + yamlString(Schema) + "\nrecords:")
	if len(records) == 0 {
		yaml.WriteString(" []\n")
	}
	for _, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil {
			return rendered{}, err
		}
		jsonl.Write(encoded)
		jsonl.WriteByte('\n')
		fmt.Fprintf(&markdown, "| %s | %s | %s | %s | %s | %d | %s |\n", markdownCell(record.MemoryID), markdownCell(record.Kind), markdownCell(record.State), markdownCell(record.Scope), markdownCell(record.Statement), record.Revision, markdownCell(record.UpdatedAt))
		writeYAMLRecord(&yaml, record)
	}
	return rendered{jsonl: jsonl.String(), markdown: markdown.String(), yaml: yaml.String()}, nil
}

func writeYAMLRecord(builder *strings.Builder, record projectedRecord) {
	fmt.Fprintf(builder, "\n  - schema: %s\n", yamlString(record.Schema))
	fmt.Fprintf(builder, "    memory_id: %s\n", yamlString(record.MemoryID))
	fmt.Fprintf(builder, "    kind: %s\n", yamlString(record.Kind))
	fmt.Fprintf(builder, "    state: %s\n", yamlString(record.State))
	fmt.Fprintf(builder, "    scope: %s\n", yamlString(record.Scope))
	fmt.Fprintf(builder, "    statement: %s\n", yamlString(record.Statement))
	fmt.Fprintf(builder, "    rationale: %s\n", yamlString(record.Rationale))
	builder.WriteString("    goal_terms:")
	if len(record.GoalTerms) == 0 {
		builder.WriteString(" []\n")
	} else {
		builder.WriteByte('\n')
		for _, term := range record.GoalTerms {
			fmt.Fprintf(builder, "      - %s\n", yamlString(term))
		}
	}
	fmt.Fprintf(builder, "    source_actor: %s\n", yamlString(record.SourceActor))
	fmt.Fprintf(builder, "    source_ref: %s\n", yamlString(record.SourceRef))
	fmt.Fprintf(builder, "    evidence_count: %d\n", record.EvidenceCount)
	fmt.Fprintf(builder, "    confidence: %d\n", record.Confidence)
	fmt.Fprintf(builder, "    revision: %d\n", record.Revision)
	fmt.Fprintf(builder, "    created_at: %s\n", yamlString(record.CreatedAt))
	fmt.Fprintf(builder, "    updated_at: %s\n", yamlString(record.UpdatedAt))
	fmt.Fprintf(builder, "    reviewed_at: %s\n", yamlString(record.ReviewedAt))
	if record.ExpiresAt != "" {
		fmt.Fprintf(builder, "    expires_at: %s\n", yamlString(record.ExpiresAt))
	}
	if record.SupersededBy != "" {
		fmt.Fprintf(builder, "    superseded_by: %s\n", yamlString(record.SupersededBy))
	}
}

func yamlString(value string) string { return strconv.Quote(value) }

func markdownCell(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r\n", "<br>")
	value = strings.ReplaceAll(value, "\n", "<br>")
	value = strings.ReplaceAll(value, "\r", "<br>")
	return value
}
