package gitcli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/worktree"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/repository"
)

func (m *WorktreeManager) Apply(ctx context.Context, record worktree.Record, expectedPatchHash string) error {
	if len(expectedPatchHash) != 64 {
		return repository.ErrPatchConflict
	}
	if err := m.requirePatchState(ctx, record, expectedPatchHash); err != nil {
		return repository.ErrPatchConflict
	}
	primary, err := canonicalDirectory(record.RepositoryRoot)
	if err != nil || filepath.Clean(primary) != filepath.Clean(record.RepositoryRoot) {
		return repository.ErrPatchConflict
	}
	head, err := m.git(ctx, primary, "rev-parse", "HEAD")
	if err != nil || head.exitCode != 0 || strings.TrimSpace(string(head.stdout)) != record.BaselineCommit {
		return repository.ErrPatchConflict
	}
	status, err := m.git(ctx, primary, "status", "--porcelain=v2", "-z", "--untracked-files=all")
	if err != nil || status.exitCode != 0 || len(status.stdout) != 0 {
		return repository.ErrPatchConflict
	}
	patch, err := m.buildBinaryPatch(ctx, record)
	if err != nil || len(patch) == 0 {
		return repository.ErrPatchApplyFailed
	}
	if err := m.requirePatchState(ctx, record, expectedPatchHash); err != nil {
		return repository.ErrPatchConflict
	}
	checked, err := m.gitInput(ctx, primary, patch, "apply", "--check", "--binary", "--whitespace=nowarn", "-")
	if err != nil || checked.exitCode != 0 {
		return repository.ErrPatchApplyFailed
	}
	applied, err := m.gitInput(ctx, primary, patch, "apply", "--binary", "--whitespace=nowarn", "-")
	if err != nil || applied.exitCode != 0 {
		return repository.ErrPatchOutcomeUnknown
	}
	return nil
}

func (m *WorktreeManager) Discard(ctx context.Context, record worktree.Record, expectedPatchHash string) error {
	if err := m.requirePatchState(ctx, record, expectedPatchHash); err != nil {
		return repository.ErrPatchConflict
	}
	if err := m.Remove(ctx, record); err != nil {
		return repository.ErrPatchOutcomeUnknown
	}
	return nil
}

func (m *WorktreeManager) requirePatchState(ctx context.Context, record worktree.Record, expectedPatchHash string) error {
	if len(expectedPatchHash) != 64 {
		return repository.ErrPatchConflict
	}
	state, err := m.Snapshot(ctx, record)
	if err != nil || patchHashForState(state.Hash) != expectedPatchHash {
		return repository.ErrPatchConflict
	}
	return nil
}

func (m *WorktreeManager) buildBinaryPatch(ctx context.Context, record worktree.Record) ([]byte, error) {
	filters, err := m.git(ctx, record.Root, "config", "--local", "--get-regexp", `^filter\..*\.(clean|smudge|process)$`)
	if (err != nil && filters.exitCode != 1) || (filters.exitCode == 0 && len(strings.TrimSpace(string(filters.stdout))) != 0) {
		return nil, repository.ErrWorktreeUnsafeConfig
	}
	file, err := os.CreateTemp(m.ownedRoot, ".talos-index-*")
	if err != nil {
		return nil, repository.ErrPatchApplyFailed
	}
	indexPath := file.Name()
	if closeErr := file.Close(); closeErr != nil {
		_ = os.Remove(indexPath)
		return nil, repository.ErrPatchApplyFailed
	}
	if err := os.Remove(indexPath); err != nil {
		return nil, repository.ErrPatchApplyFailed
	}
	defer os.Remove(indexPath)
	extra := []string{"GIT_INDEX_FILE=" + indexPath}
	if result, err := m.gitWithEnvironment(ctx, record.Root, extra, "read-tree", record.BaselineCommit); err != nil || result.exitCode != 0 {
		return nil, repository.ErrPatchApplyFailed
	}
	if result, err := m.gitWithEnvironment(ctx, record.Root, extra, "add", "-A", "--", "."); err != nil || result.exitCode != 0 {
		return nil, repository.ErrPatchApplyFailed
	}
	result, err := m.gitWithEnvironment(ctx, record.Root, extra, "diff", "--cached", "--binary", "--full-index", "--no-ext-diff", "--no-textconv", record.BaselineCommit, "--")
	if err != nil || result.exitCode != 0 {
		return nil, repository.ErrPatchApplyFailed
	}
	return append([]byte(nil), result.stdout...), nil
}

func (m *WorktreeManager) gitWithEnvironment(ctx context.Context, root string, extra []string, command ...string) (result, error) {
	commandCtx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	args := []string{"--no-pager", "--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "color.ui=false", "-c", "core.quotepath=false", "-C", root}
	args = append(args, command...)
	result, err := m.run(commandCtx, m.executable, args, append(gitEnvironment(), extra...))
	if errors.Is(err, errOutputLimit) {
		return result, repository.ErrOutputLimit
	}
	if commandCtx.Err() != nil {
		return result, repository.ErrPatchApplyFailed
	}
	return result, err
}

func (m *WorktreeManager) gitInput(ctx context.Context, root string, input []byte, command ...string) (result, error) {
	commandCtx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	args := []string{"--no-pager", "--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "color.ui=false", "-C", root}
	args = append(args, command...)
	process := exec.CommandContext(commandCtx, m.executable, args...)
	process.Env = gitEnvironment()
	process.Stdin = bytes.NewReader(input)
	stdout := &limitedBuffer{limit: maxStdoutBytes}
	stderr := &limitedBuffer{limit: maxStderrBytes}
	process.Stdout, process.Stderr = stdout, stderr
	err := process.Run()
	exitCode := 0
	if err != nil {
		exitCode = -1
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
		}
	}
	completed := result{stdout: stdout.buffer.Bytes(), stderr: stderr.buffer.Bytes(), exitCode: exitCode}
	if stdout.exceeded || stderr.exceeded {
		return completed, repository.ErrOutputLimit
	}
	if commandCtx.Err() != nil {
		return completed, repository.ErrPatchApplyFailed
	}
	return completed, err
}
