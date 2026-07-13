package gitcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/worktree"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/repository"
)

const worktreeMarkerSchema = "talos.task-worktree-owner/1"

type worktreeMarker struct {
	Schema         string `json:"schema"`
	TaskID         string `json:"task_id"`
	RepositoryRoot string `json:"repository_root"`
	WorktreeRoot   string `json:"worktree_root"`
	BaselineCommit string `json:"baseline_commit"`
	CreatedAt      string `json:"created_at"`
}

type WorktreeManager struct {
	executable string
	ownedRoot  string
	timeout    time.Duration
	now        func() time.Time
	run        runner
}

func NewWorktreeManager(ownedRoot string) (*WorktreeManager, error) {
	executable, err := exec.LookPath("git")
	if err != nil {
		return nil, repository.ErrGitUnavailable
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return nil, repository.ErrGitUnavailable
	}
	if ownedRoot == "" || strings.IndexByte(ownedRoot, 0) >= 0 {
		return nil, repository.ErrInvalidPath
	}
	absolute, err := filepath.Abs(ownedRoot)
	if err != nil {
		return nil, repository.ErrInvalidPath
	}
	for _, child := range []string{"worktrees", "owners", "hooks-empty"} {
		if err := os.MkdirAll(filepath.Join(absolute, child), 0o700); err != nil {
			return nil, fmt.Errorf("initialize task worktree storage: %w", err)
		}
	}
	canonical, err := canonicalDirectory(absolute)
	if err != nil {
		return nil, err
	}
	return &WorktreeManager{executable: executable, ownedRoot: canonical, timeout: 30 * time.Second, now: func() time.Time { return time.Now().UTC() }, run: runCommand}, nil
}

func (m *WorktreeManager) Create(ctx context.Context, input repository.CreateWorktreeInput) (worktree.Record, error) {
	createdAt := input.CreatedAt
	if createdAt.IsZero() {
		createdAt = m.now()
	}
	primary, err := canonicalDirectory(input.RepositoryRoot)
	if err != nil {
		return worktree.Record{}, err
	}
	destination, markerPath, err := m.paths(input.TaskID)
	if err != nil {
		return worktree.Record{}, err
	}
	record := worktree.Record{TaskID: input.TaskID, RepositoryRoot: primary, Root: destination, BaselineCommit: input.BaselineCommit, CreatedAt: createdAt.UTC()}
	if err := record.Validate(); err != nil {
		return worktree.Record{}, repository.ErrInvalidPath
	}
	if _, err := os.Lstat(destination); err == nil {
		return worktree.Record{}, repository.ErrWorktreeExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return worktree.Record{}, repository.ErrWorktreeCreateFailed
	}
	if _, err := os.Lstat(markerPath); err == nil {
		return worktree.Record{}, repository.ErrWorktreeExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return worktree.Record{}, repository.ErrWorktreeCreateFailed
	}
	resolved, err := m.git(ctx, primary, "rev-parse", "--verify", input.BaselineCommit+"^{commit}")
	if err != nil || resolved.exitCode != 0 || strings.TrimSpace(string(resolved.stdout)) != input.BaselineCommit {
		return worktree.Record{}, repository.ErrNoBaselineCommit
	}
	filters, err := m.git(ctx, primary, "config", "--local", "--get-regexp", `^filter\..*\.(clean|smudge|process)$`)
	if filters.exitCode != 0 && filters.exitCode != 1 {
		return worktree.Record{}, fmt.Errorf("%w: inspect checkout filters", repository.ErrWorktreeCreateFailed)
	}
	if filters.exitCode == 0 && len(strings.TrimSpace(string(filters.stdout))) > 0 {
		return worktree.Record{}, repository.ErrWorktreeUnsafeConfig
	}
	added, err := m.git(ctx, primary, "-c", "core.longpaths=true", "-c", "core.hooksPath="+filepath.Join(m.ownedRoot, "hooks-empty"), "worktree", "add", "--detach", "--no-checkout", destination, input.BaselineCommit)
	if err != nil || added.exitCode != 0 {
		m.cleanupPartial(primary, destination)
		return worktree.Record{}, fmt.Errorf("%w: register detached worktree", repository.ErrWorktreeCreateFailed)
	}
	checkedOut, err := m.git(ctx, destination, "-c", "core.longpaths=true", "-c", "core.hooksPath="+filepath.Join(m.ownedRoot, "hooks-empty"), "reset", "--hard", input.BaselineCommit)
	if err != nil || checkedOut.exitCode != 0 {
		m.cleanupPartial(primary, destination)
		return worktree.Record{}, fmt.Errorf("%w: materialize baseline", repository.ErrWorktreeCreateFailed)
	}
	marker := worktreeMarker{Schema: worktreeMarkerSchema, TaskID: record.TaskID, RepositoryRoot: record.RepositoryRoot, WorktreeRoot: record.Root, BaselineCommit: record.BaselineCommit, CreatedAt: record.CreatedAt.Format(time.RFC3339Nano)}
	encoded, err := json.Marshal(marker)
	if err != nil {
		m.cleanupPartial(primary, destination)
		return worktree.Record{}, fmt.Errorf("%w: encode owner marker", repository.ErrWorktreeCreateFailed)
	}
	file, err := os.OpenFile(markerPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		m.cleanupPartial(primary, destination)
		return worktree.Record{}, fmt.Errorf("%w: create owner marker", repository.ErrWorktreeCreateFailed)
	}
	if _, err = file.Write(encoded); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(markerPath)
		m.cleanupPartial(primary, destination)
		return worktree.Record{}, fmt.Errorf("%w: persist owner marker", repository.ErrWorktreeCreateFailed)
	}
	return record, nil
}

func (m *WorktreeManager) Open(ctx context.Context, input repository.CreateWorktreeInput) (worktree.Record, error) {
	primary, err := canonicalDirectory(input.RepositoryRoot)
	if err != nil {
		return worktree.Record{}, repository.ErrWorktreeOwnership
	}
	destination, markerPath, err := m.paths(input.TaskID)
	if err != nil {
		return worktree.Record{}, repository.ErrWorktreeOwnership
	}
	marker, err := readWorktreeMarker(markerPath)
	if err != nil || marker.Schema != worktreeMarkerSchema || marker.TaskID != input.TaskID || marker.RepositoryRoot != primary || marker.WorktreeRoot != destination || marker.BaselineCommit != input.BaselineCommit {
		return worktree.Record{}, repository.ErrWorktreeOwnership
	}
	createdAt, err := time.Parse(time.RFC3339Nano, marker.CreatedAt)
	if err != nil {
		return worktree.Record{}, repository.ErrWorktreeOwnership
	}
	canonicalWorktree, err := canonicalDirectory(destination)
	if err != nil || filepath.Clean(canonicalWorktree) != filepath.Clean(destination) || !containsPath(filepath.Join(m.ownedRoot, "worktrees"), canonicalWorktree) {
		return worktree.Record{}, repository.ErrWorktreeOwnership
	}
	record := worktree.Record{TaskID: input.TaskID, RepositoryRoot: primary, Root: canonicalWorktree, BaselineCommit: input.BaselineCommit, CreatedAt: createdAt.UTC()}
	if err := record.Validate(); err != nil {
		return worktree.Record{}, repository.ErrWorktreeOwnership
	}
	root, err := m.git(ctx, record.Root, "rev-parse", "--show-toplevel")
	if err != nil || root.exitCode != 0 {
		return worktree.Record{}, repository.ErrWorktreeOwnership
	}
	reportedRoot, err := canonicalDirectory(strings.TrimSpace(string(root.stdout)))
	if err != nil || filepath.Clean(reportedRoot) != filepath.Clean(record.Root) {
		return worktree.Record{}, repository.ErrWorktreeOwnership
	}
	head, err := m.git(ctx, record.Root, "rev-parse", "HEAD")
	if err != nil || head.exitCode != 0 || strings.TrimSpace(string(head.stdout)) != record.BaselineCommit {
		return worktree.Record{}, repository.ErrWorktreeOwnership
	}
	return record, nil
}

func (m *WorktreeManager) Remove(ctx context.Context, record worktree.Record) error {
	if err := record.Validate(); err != nil {
		return repository.ErrWorktreeOwnership
	}
	destination, markerPath, err := m.paths(record.TaskID)
	if err != nil || filepath.Clean(destination) != filepath.Clean(record.Root) {
		return repository.ErrWorktreeOwnership
	}
	marker, err := readWorktreeMarker(markerPath)
	if err != nil || marker.Schema != worktreeMarkerSchema || marker.TaskID != record.TaskID || filepath.Clean(marker.RepositoryRoot) != filepath.Clean(record.RepositoryRoot) || filepath.Clean(marker.WorktreeRoot) != filepath.Clean(record.Root) || marker.BaselineCommit != record.BaselineCommit {
		return repository.ErrWorktreeOwnership
	}
	primary, err := canonicalDirectory(record.RepositoryRoot)
	if err != nil {
		return repository.ErrWorktreeOwnership
	}
	removed, runErr := m.git(ctx, primary, "worktree", "remove", "--force", destination)
	if runErr != nil || removed.exitCode != 0 {
		return repository.ErrWorktreeRemoveFailed
	}
	if err := os.Remove(markerPath); err != nil {
		return repository.ErrWorktreeRemoveFailed
	}
	return nil
}

func (m *WorktreeManager) paths(taskID string) (string, string, error) {
	probe := worktree.Record{TaskID: taskID, RepositoryRoot: filepath.Join(m.ownedRoot, "primary"), Root: filepath.Join(m.ownedRoot, "worktrees", taskID), BaselineCommit: strings.Repeat("a", 40), CreatedAt: time.Unix(1, 0)}
	if err := probe.Validate(); err != nil {
		return "", "", repository.ErrInvalidPath
	}
	destination := filepath.Join(m.ownedRoot, "worktrees", taskID)
	marker := filepath.Join(m.ownedRoot, "owners", taskID+".json")
	if !containsPath(m.ownedRoot, destination) || !containsPath(m.ownedRoot, marker) {
		return "", "", repository.ErrInvalidPath
	}
	return destination, marker, nil
}

func (m *WorktreeManager) git(ctx context.Context, root string, command ...string) (result, error) {
	commandCtx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	args := []string{"--no-pager", "--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "color.ui=false", "-c", "core.quotepath=false", "-C", root}
	args = append(args, command...)
	result, err := m.run(commandCtx, m.executable, args, gitEnvironment())
	if errors.Is(err, errOutputLimit) {
		return result, repository.ErrOutputLimit
	}
	if commandCtx.Err() != nil {
		return result, repository.ErrWorktreeCreateFailed
	}
	return result, err
}

func (m *WorktreeManager) cleanupPartial(primary, destination string) {
	if !containsPath(filepath.Join(m.ownedRoot, "worktrees"), destination) {
		return
	}
	_, _ = m.git(context.Background(), primary, "worktree", "remove", "--force", destination)
	if _, err := os.Lstat(destination); err == nil {
		_ = os.RemoveAll(destination)
	}
}

func readWorktreeMarker(path string) (worktreeMarker, error) {
	payload, err := os.ReadFile(path)
	if err != nil || len(payload) > 4096 {
		return worktreeMarker{}, repository.ErrWorktreeOwnership
	}
	var marker worktreeMarker
	if json.Unmarshal(payload, &marker) != nil {
		return worktreeMarker{}, repository.ErrWorktreeOwnership
	}
	return marker, nil
}
