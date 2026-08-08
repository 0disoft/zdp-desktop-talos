package wailsapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspace"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/repository"
)

func TestWorkspaceServiceInspectsAndClosesRepository(t *testing.T) {
	t.Parallel()
	snapshot := workspace.RepositorySnapshot{
		Root: `C:\repo`, BaselineCommit: strings.Repeat("a", 40), HeadRef: "main",
		Dirty: true, Changes: []workspace.Change{{Path: "main.go", Kind: workspace.ChangeTracked, IndexStatus: 'M', WorktreeStatus: '.'}},
		CapturedAt: time.Unix(1_800_000_000, 0).UTC(),
	}
	service := NewWorkspaceService(fakeInspector{snapshot: snapshot}, nil)
	opened := service.InspectRepository(`C:\repo\src`, "inspect")
	if opened.Error != nil || opened.Workspace == nil || opened.Workspace.State != "open" || opened.Workspace.ChangeCount != 1 || opened.Workspace.BaselineCommit != snapshot.BaselineCommit {
		t.Fatalf("opened=%+v", opened)
	}
	closed := service.Close()
	if closed.Error != nil || closed.Workspace == nil || closed.Workspace.State != "closed" || service.Status().State != "closed" {
		t.Fatalf("closed=%+v", closed)
	}
}

func TestWorkspaceServiceMapsInspectionFailuresSafely(t *testing.T) {
	t.Parallel()
	service := NewWorkspaceService(fakeInspector{err: repository.ErrNoBaselineCommit}, nil)
	result := service.InspectRepository(`C:\private\repo`, "workspace")
	if result.Error == nil || result.Error.Code != "WORKSPACE_BASELINE_MISSING" || result.Error.CorrelationID != "workspace" {
		t.Fatalf("result=%+v", result)
	}
	if result.Error.Message == `C:\private\repo` {
		t.Fatal("private path leaked through public error")
	}
	unavailable := NewWorkspaceService(nil, errors.New(`C:\private\git.exe`)).InspectRepository(`C:\repo`, "git")
	if unavailable.Error == nil || unavailable.Error.Code != "GIT_UNAVAILABLE" {
		t.Fatalf("unavailable=%+v", unavailable)
	}
}

type fakeInspector struct {
	snapshot workspace.RepositorySnapshot
	err      error
}

func openWorkspaceForTest(t *testing.T, root, baseline string) *WorkspaceService {
	t.Helper()
	snapshot := workspace.RepositorySnapshot{Root: root, BaselineCommit: baseline, HeadRef: "main", CapturedAt: time.Now().UTC()}
	service := NewWorkspaceService(fakeInspector{snapshot: snapshot}, nil)
	result := service.InspectRepository(root, "test-workspace")
	if result.Error != nil || result.Workspace == nil {
		t.Fatalf("open workspace=%+v", result)
	}
	return service
}

func (f fakeInspector) Inspect(context.Context, string) (workspace.RepositorySnapshot, error) {
	return f.snapshot, f.err
}
