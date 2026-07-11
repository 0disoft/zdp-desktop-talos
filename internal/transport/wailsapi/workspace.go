package wailsapi

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspace"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/repository"
)

var (
	errWorkspaceNotOpen = errors.New("workspace is not open")
	errWorkspaceDirty   = errors.New("workspace has uncommitted changes")
	errWorkspaceChanged = errors.New("workspace baseline changed")
)

type WorkspaceStatus struct {
	State          string `json:"state"`
	Root           string `json:"root,omitempty"`
	BaselineCommit string `json:"baseline_commit,omitempty"`
	HeadRef        string `json:"head_ref,omitempty"`
	Detached       bool   `json:"detached,omitempty"`
	Dirty          bool   `json:"dirty,omitempty"`
	ChangeCount    int    `json:"change_count,omitempty"`
	CapturedAt     string `json:"captured_at,omitempty"`
}

type WorkspaceResult struct {
	Workspace *WorkspaceStatus `json:"workspace,omitempty"`
	Error     *TalosError      `json:"error,omitempty"`
}

type WorkspaceService struct {
	mu                  sync.Mutex
	inspector           repository.Inspector
	initializationError error
	snapshot            *workspace.RepositorySnapshot
}

func NewWorkspaceService(inspector repository.Inspector, initializationError error) *WorkspaceService {
	return &WorkspaceService{inspector: inspector, initializationError: initializationError}
}

func (s *WorkspaceService) Status() WorkspaceStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return workspaceStatus(s.snapshot)
}

func (s *WorkspaceService) InspectRepository(path, correlationID string) WorkspaceResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inspector == nil {
		err := s.initializationError
		if err == nil {
			err = errors.New("repository inspector is unavailable")
		}
		mapped := MapError(err, correlationID)
		mapped.Code = "GIT_UNAVAILABLE"
		mapped.Message = "이 기기에서 시스템 Git을 사용할 수 없습니다."
		return WorkspaceResult{Error: &mapped}
	}
	snapshot, err := s.inspector.Inspect(context.Background(), path)
	if err != nil {
		mapped := MapError(err, correlationID)
		return WorkspaceResult{Error: &mapped}
	}
	s.snapshot = &snapshot
	status := workspaceStatus(s.snapshot)
	return WorkspaceResult{Workspace: &status}
}

func (s *WorkspaceService) Close() WorkspaceResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot = nil
	status := workspaceStatus(nil)
	return WorkspaceResult{Workspace: &status}
}

func (s *WorkspaceService) contractSnapshot() (workspace.RepositorySnapshot, error) {
	return s.currentSnapshot(true)
}

func (s *WorkspaceService) decisionSnapshot() (workspace.RepositorySnapshot, error) {
	return s.currentSnapshot(false)
}

func (s *WorkspaceService) currentSnapshot(requireClean bool) (workspace.RepositorySnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot == nil {
		return workspace.RepositorySnapshot{}, errWorkspaceNotOpen
	}
	current, err := s.inspector.Inspect(context.Background(), s.snapshot.Root)
	if err != nil {
		return workspace.RepositorySnapshot{}, err
	}
	previous := *s.snapshot
	s.snapshot = &current
	if current.Root != previous.Root || current.BaselineCommit != previous.BaselineCommit {
		return workspace.RepositorySnapshot{}, errWorkspaceChanged
	}
	if requireClean && current.Dirty {
		return workspace.RepositorySnapshot{}, errWorkspaceDirty
	}
	return current, nil
}

func workspaceStatus(snapshot *workspace.RepositorySnapshot) WorkspaceStatus {
	if snapshot == nil {
		return WorkspaceStatus{State: "closed"}
	}
	return WorkspaceStatus{
		State: "open", Root: snapshot.Root, BaselineCommit: snapshot.BaselineCommit,
		HeadRef: snapshot.HeadRef, Detached: snapshot.Detached, Dirty: snapshot.Dirty,
		ChangeCount: len(snapshot.Changes), CapturedAt: snapshot.CapturedAt.UTC().Format(time.RFC3339Nano),
	}
}
