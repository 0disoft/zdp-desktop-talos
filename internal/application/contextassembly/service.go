package contextassembly

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspacemapping"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorycontext"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorystore"
)

var ErrInvalidRequest = errors.New("invalid memory context request")

type Service struct {
	store memorystore.Reader
	now   func() time.Time
}

func New(store memorystore.Reader) (*Service, error) {
	if store == nil {
		return nil, ErrInvalidRequest
	}
	return &Service{store: store, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *Service) Assemble(ctx context.Context, request memorycontext.Request) (memorycontext.Result, error) {
	if ctx == nil || strings.TrimSpace(request.VaultID) == "" || !workspacemapping.ValidWorkspaceID(request.WorkspaceID) || strings.TrimSpace(request.Goal) == "" || request.MaxCandidates < 1 || request.MaxCandidates > 200 || request.MaxItems < 1 || request.MaxItems > 32 || request.MaxCandidates < request.MaxItems || request.MaxBytes < 256 || request.MaxBytes > 256<<10 {
		return memorycontext.Result{}, ErrInvalidRequest
	}
	now := s.now().UTC()
	records, err := s.store.ListActiveMemories(ctx, memorystore.ListActiveInput{VaultID: request.VaultID, WorkspaceID: request.WorkspaceID, Limit: request.MaxCandidates, At: now})
	if err != nil {
		return memorycontext.Result{}, err
	}
	haystack := strings.ToLower(strings.Join(append([]string{request.Goal}, request.AllowedPaths...), "\n"))
	result := memorycontext.Result{Items: make([]memorycontext.Item, 0, request.MaxItems), Considered: len(records)}
	for _, record := range records {
		if record.Validate() != nil || record.VaultID != request.VaultID {
			return memorycontext.Result{}, ErrInvalidRequest
		}
		if record.IsExpired(now) {
			continue
		}
		if record.Scope.Kind == memory.ScopeWorkspace {
			if record.Scope.WorkspaceID != request.WorkspaceID {
				return memorycontext.Result{}, ErrInvalidRequest
			}
		}
		reason, applies := applicabilityReason(record, haystack)
		if !applies {
			continue
		}
		content := strings.TrimSpace(record.Statement)
		if result.SelectedBytes+len(content) > request.MaxBytes {
			continue
		}
		result.Items = append(result.Items, memorycontext.Item{
			ID: "memory-" + record.ID, MemoryID: record.ID, Revision: record.Revision,
			Kind: "approved_memory", SourceRef: fmt.Sprintf("memory:%s:revision:%d", record.ID, record.Revision),
			Sensitivity: record.Sensitivity, Content: content, Reason: reason,
		})
		result.SelectedBytes += len(content)
		if len(result.Items) == request.MaxItems {
			break
		}
	}
	return result, nil
}

func applicabilityReason(record memory.Record, haystack string) (string, bool) {
	if record.State != memory.StateApproved && record.State != memory.StateStable {
		return "", false
	}
	if len(record.Applicability.GoalTerms) == 0 {
		return fmt.Sprintf("%s memory matched %s scope", record.State, record.Scope.Kind), true
	}
	for _, term := range record.Applicability.GoalTerms {
		if strings.Contains(haystack, term) {
			return fmt.Sprintf("%s memory matched goal term %q", record.State, term), true
		}
	}
	return "", false
}
