package wailsapi

import (
	"context"
	"strings"

	"github.com/0disoft/zdp-desktop-talos/internal/application/memorykernel"
	"github.com/0disoft/zdp-desktop-talos/internal/application/memoryprojection"
	"github.com/0disoft/zdp-desktop-talos/internal/security/redaction"
)

const (
	maxProjectionPreviewMemories = 200
	maxProjectionPreviewBytes    = 64 << 10
)

type ProjectionFileDTO struct {
	Path      string `json:"path"`
	MediaType string `json:"media_type"`
	SHA256    string `json:"sha256"`
	SizeBytes int    `json:"size_bytes"`
	Preview   string `json:"preview"`
	Truncated bool   `json:"truncated"`
}

type ProjectionPreviewResult struct {
	Schema              string              `json:"schema"`
	BundleSHA256        string              `json:"bundle_sha256"`
	Included            int                 `json:"included"`
	ExcludedSensitivity int                 `json:"excluded_sensitivity"`
	ExcludedLifecycle   int                 `json:"excluded_lifecycle"`
	Complete            bool                `json:"complete"`
	Files               []ProjectionFileDTO `json:"files"`
	Error               *TalosError         `json:"error,omitempty"`
}

type ProjectionService struct{ vault *VaultService }

func NewProjectionService(vault *VaultService) *ProjectionService {
	return &ProjectionService{vault: vault}
}

func (s *ProjectionService) Preview(correlationID string) ProjectionPreviewResult {
	correlationID = normalizeCorrelationID(correlationID)
	if s.vault == nil {
		return projectionPreviewError(memoryprojection.ErrInvalidRequest, correlationID)
	}
	projection, complete, err := s.vault.compileMemoryProjection(maxProjectionPreviewMemories)
	if err != nil {
		return projectionPreviewError(err, correlationID)
	}
	result := ProjectionPreviewResult{
		Schema: projection.Schema, BundleSHA256: projection.BundleSHA256, Included: projection.Included,
		ExcludedSensitivity: projection.ExcludedSensitivity, ExcludedLifecycle: projection.ExcludedLifecycle,
		Complete: complete, Files: make([]ProjectionFileDTO, 0, len(projection.Files)),
	}
	for _, file := range projection.Files {
		preview := file.Content
		truncated := len(preview) > maxProjectionPreviewBytes
		if truncated {
			preview = preview[:maxProjectionPreviewBytes]
			preview = strings.ToValidUTF8(preview, "�")
		}
		result.Files = append(result.Files, ProjectionFileDTO{Path: file.Path, MediaType: file.MediaType, SHA256: file.SHA256, SizeBytes: len(file.Content), Preview: preview, Truncated: truncated})
	}
	return result
}

func (s *VaultService) compileMemoryProjection(limit int) (memoryprojection.Result, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	database, vaultID, err := s.memoryDatabaseLocked()
	if err != nil {
		return memoryprojection.Result{}, false, err
	}
	kernel, err := memorykernel.New(database)
	if err != nil {
		return memoryprojection.Result{}, false, err
	}
	records, err := kernel.ListAll(context.Background(), vaultID, limit)
	if err != nil {
		return memoryprojection.Result{}, false, err
	}
	compiler, err := memoryprojection.New(redaction.NewScanner())
	if err != nil {
		return memoryprojection.Result{}, false, err
	}
	result, err := compiler.Compile(context.Background(), records)
	return result, len(records) < limit, err
}

func projectionPreviewError(err error, correlationID string) ProjectionPreviewResult {
	mapped := MapError(err, correlationID)
	return ProjectionPreviewResult{Error: &mapped}
}
