package wailsapi

import (
	"context"
	"strings"

	"github.com/0disoft/zdp-desktop-talos/internal/application/permissionreview"
	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/permission"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/executionstore"
)

type PermissionRequestDTO struct {
	RequestID        string   `json:"request_id"`
	TaskID           string   `json:"task_id"`
	RuleID           string   `json:"rule_id"`
	Executable       string   `json:"executable"`
	Arguments        []string `json:"arguments"`
	EnvironmentNames []string `json:"environment_names"`
	TimeoutMS        int64    `json:"timeout_ms"`
	MaxOutputBytes   int      `json:"max_output_bytes"`
	CreatedAt        string   `json:"created_at"`
}

type PermissionListResult struct {
	Requests []PermissionRequestDTO `json:"requests"`
	Error    *TalosError            `json:"error,omitempty"`
}

type PermissionResolveRequest struct {
	RequestID     string `json:"request_id"`
	Outcome       string `json:"outcome"`
	RequestToken  string `json:"request_token"`
	CorrelationID string `json:"correlation_id"`
}

type PermissionResolutionDTO struct {
	RequestID string `json:"request_id"`
	State     string `json:"state"`
	Outcome   string `json:"outcome"`
	ExpiresAt string `json:"expires_at"`
}

type PermissionResult struct {
	Resolution *PermissionResolutionDTO `json:"resolution,omitempty"`
	Error      *TalosError              `json:"error,omitempty"`
}

type PermissionService struct{ vault *VaultService }

func NewPermissionService(vault *VaultService) *PermissionService {
	return &PermissionService{vault: vault}
}

func (s *PermissionService) List(taskID, correlationID string) PermissionListResult {
	correlationID = normalizeCorrelationID(correlationID)
	if s.vault == nil || strings.TrimSpace(taskID) == "" {
		mapped := MapError(permissionreview.ErrInvalidResolution, correlationID)
		return PermissionListResult{Requests: []PermissionRequestDTO{}, Error: &mapped}
	}
	requests, err := s.vault.listPermissionRequests(strings.TrimSpace(taskID))
	if err != nil {
		mapped := MapError(err, correlationID)
		return PermissionListResult{Requests: []PermissionRequestDTO{}, Error: &mapped}
	}
	dtos := make([]PermissionRequestDTO, 0, len(requests))
	for _, request := range requests {
		dtos = append(dtos, permissionRequestDTO(request))
	}
	return PermissionListResult{Requests: dtos}
}

func (s *PermissionService) Resolve(request PermissionResolveRequest) PermissionResult {
	correlationID := normalizeCorrelationID(request.CorrelationID)
	if s.vault == nil || strings.TrimSpace(request.RequestID) == "" || strings.TrimSpace(request.RequestToken) == "" {
		mapped := MapError(permissionreview.ErrInvalidResolution, correlationID)
		return PermissionResult{Error: &mapped}
	}
	resolution, err := s.vault.resolvePermissionRequest(strings.TrimSpace(request.RequestID), permission.Outcome(request.Outcome), "permission-review:"+strings.TrimSpace(request.RequestToken))
	if err != nil {
		mapped := MapError(err, correlationID)
		return PermissionResult{Error: &mapped}
	}
	dto := PermissionResolutionDTO{RequestID: resolution.Request.ID, State: string(resolution.Request.State), Outcome: string(resolution.Grant.Outcome), ExpiresAt: resolution.Grant.ExpiresAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")}
	return PermissionResult{Resolution: &dto}
}

func permissionRequestDTO(request permission.Request) PermissionRequestDTO {
	return PermissionRequestDTO{RequestID: request.ID, TaskID: request.TaskID, RuleID: request.Intent.RuleID, Executable: request.Intent.Executable, Arguments: append([]string(nil), request.Intent.Arguments...), EnvironmentNames: append([]string(nil), request.Intent.EnvironmentNames...), TimeoutMS: request.Intent.Timeout.Milliseconds(), MaxOutputBytes: request.Intent.MaxOutputBytes, CreatedAt: request.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")}
}

func (s *VaultService) listPermissionRequests(taskID string) ([]permission.Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		return nil, vaultbootstrap.ErrNotOpen
	}
	database, err := s.session.ExecutionDatabase()
	if err != nil {
		return nil, err
	}
	service, err := permissionreview.New(database)
	if err != nil {
		return nil, err
	}
	return service.List(context.Background(), s.session.Record.ID, taskID)
}

func (s *VaultService) resolvePermissionRequest(requestID string, outcome permission.Outcome, key string) (executionstore.PermissionResolution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		return executionstore.PermissionResolution{}, vaultbootstrap.ErrNotOpen
	}
	database, err := s.session.ExecutionDatabase()
	if err != nil {
		return executionstore.PermissionResolution{}, err
	}
	service, err := permissionreview.New(database)
	if err != nil {
		return executionstore.PermissionResolution{}, err
	}
	return service.Resolve(context.Background(), s.session.Record.ID, requestID, outcome, key)
}
