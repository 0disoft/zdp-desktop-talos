package workspaceremap

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspace"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspacemapping"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/workspacestore"
)

func TestMapInspectsCanonicalRepositoryAndRequiresBaseline(t *testing.T) {
	root := filepath.Join(t.TempDir(), "local-clone")
	sourceHash := strings.Repeat("a", 64)
	baseline := strings.Repeat("b", 40)
	workspaceID := workspacemapping.ID("vault", sourceHash)
	store := &workspaceStoreFake{requirement: workspacemapping.Requirement{WorkspaceID: workspaceID, VaultID: "vault", SourceWorkspaceHash: sourceHash, BaselineCommit: baseline}}
	repository := &repositoryFake{snapshot: workspace.RepositorySnapshot{Root: root, BaselineCommit: strings.Repeat("c", 40)}, contains: true}
	service, err := New(store, repository, repository)
	if err != nil {
		t.Fatal(err)
	}

	mapped, replayed, err := service.Map(context.Background(), MapInput{VaultID: "vault", TaskID: "task", LocalPath: filepath.Join(root, "nested"), ExpectedRevision: 0, IdempotencyKey: "map-task"})
	if err != nil || replayed || mapped.LocalRoot != root || store.bound.LocalRoot != root || store.bound.VerifiedBaseline != baseline || repository.verifiedCommit != baseline {
		t.Fatalf("mapped=%+v replayed=%v bound=%+v verified=%q error=%v", mapped, replayed, store.bound, repository.verifiedCommit, err)
	}

	repository.contains = false
	if _, _, err := service.Map(context.Background(), MapInput{VaultID: "vault", TaskID: "task", LocalPath: root, ExpectedRevision: 1, IdempotencyKey: "map-missing"}); !errors.Is(err, ErrBaselineNotFound) {
		t.Fatalf("missing baseline error=%v", err)
	}
}

type workspaceStoreFake struct {
	requirement workspacemapping.Requirement
	bound       workspacestore.BindInput
}

func (s *workspaceStoreFake) GetTaskWorkspace(context.Context, string, string) (workspacemapping.Requirement, error) {
	return s.requirement, nil
}

func (s *workspaceStoreFake) GetWorkspaceMapping(context.Context, string, string) (workspacemapping.Record, error) {
	return workspacemapping.Record{}, workspacestore.ErrNotFound
}

func (s *workspaceStoreFake) ListTaskWorkspaces(context.Context, string, int) ([]workspacestore.TaskWorkspace, error) {
	return []workspacestore.TaskWorkspace{{TaskID: "task", Requirement: s.requirement}}, nil
}

func (s *workspaceStoreFake) BindWorkspaceMapping(_ context.Context, input workspacestore.BindInput) (workspacemapping.Record, bool, error) {
	s.bound = input
	return workspacemapping.Record{WorkspaceID: input.WorkspaceID, VaultID: input.VaultID, SourceWorkspaceHash: input.SourceWorkspaceHash, LocalRoot: input.LocalRoot, VerifiedBaseline: input.VerifiedBaseline}, false, nil
}

func (s *workspaceStoreFake) RevokeWorkspaceMapping(context.Context, workspacestore.RevokeInput) (workspacemapping.Record, bool, error) {
	return workspacemapping.Record{}, false, nil
}

type repositoryFake struct {
	snapshot       workspace.RepositorySnapshot
	contains       bool
	verifiedCommit string
}

func (r *repositoryFake) Inspect(context.Context, string) (workspace.RepositorySnapshot, error) {
	return r.snapshot, nil
}

func (r *repositoryFake) ContainsCommit(_ context.Context, _ string, commit string) (bool, error) {
	r.verifiedCommit = commit
	if !r.contains {
		return false, nil
	}
	return true, nil
}
