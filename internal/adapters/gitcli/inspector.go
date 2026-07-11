package gitcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspace"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/repository"
)

const (
	defaultTimeout = 10 * time.Second
	maxStdoutBytes = 4 << 20
	maxStderrBytes = 32 << 10
)

type result struct {
	stdout   []byte
	stderr   []byte
	exitCode int
}

type runner func(context.Context, string, []string, []string) (result, error)

type Inspector struct {
	executable string
	timeout    time.Duration
	now        func() time.Time
	run        runner
}

func New() (*Inspector, error) {
	executable, err := exec.LookPath("git")
	if err != nil {
		return nil, repository.ErrGitUnavailable
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve Git executable: %v", repository.ErrGitUnavailable, err)
	}
	return &Inspector{executable: executable, timeout: defaultTimeout, now: func() time.Time { return time.Now().UTC() }, run: runCommand}, nil
}

func (i *Inspector) Inspect(ctx context.Context, requestedPath string) (workspace.RepositorySnapshot, error) {
	requestedRoot, err := canonicalDirectory(requestedPath)
	if err != nil {
		return workspace.RepositorySnapshot{}, err
	}
	discovered, err := i.git(ctx, requestedRoot, "rev-parse", "--show-toplevel")
	if err != nil || discovered.exitCode != 0 {
		return workspace.RepositorySnapshot{}, repository.ErrNotRepository
	}
	repositoryRoot, err := canonicalDirectory(strings.TrimSpace(string(discovered.stdout)))
	if err != nil || !containsPath(repositoryRoot, requestedRoot) {
		return workspace.RepositorySnapshot{}, repository.ErrNotRepository
	}
	bare, err := i.git(ctx, repositoryRoot, "rev-parse", "--is-bare-repository")
	if err != nil || bare.exitCode != 0 || strings.TrimSpace(string(bare.stdout)) != "false" {
		return workspace.RepositorySnapshot{}, repository.ErrNotRepository
	}
	baseline, err := i.git(ctx, repositoryRoot, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || baseline.exitCode != 0 {
		return workspace.RepositorySnapshot{}, repository.ErrNoBaselineCommit
	}
	head, err := i.git(ctx, repositoryRoot, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return workspace.RepositorySnapshot{}, err
	}
	detached := head.exitCode == 1
	if head.exitCode != 0 && !detached {
		return workspace.RepositorySnapshot{}, repository.ErrInspectionFailed
	}
	status, err := i.git(ctx, repositoryRoot, "status", "--porcelain=v2", "-z", "--untracked-files=normal")
	if err != nil || status.exitCode != 0 {
		return workspace.RepositorySnapshot{}, repository.ErrInspectionFailed
	}
	changes, err := parseStatus(status.stdout)
	if err != nil {
		return workspace.RepositorySnapshot{}, err
	}
	snapshot := workspace.RepositorySnapshot{
		Root: repositoryRoot, BaselineCommit: strings.TrimSpace(string(baseline.stdout)),
		HeadRef: strings.TrimSpace(string(head.stdout)), Detached: detached,
		Dirty: len(changes) > 0, Changes: changes, CapturedAt: i.now().UTC(),
	}
	if err := snapshot.Validate(); err != nil {
		return workspace.RepositorySnapshot{}, fmt.Errorf("%w: %v", repository.ErrInspectionFailed, err)
	}
	return snapshot, nil
}

func (i *Inspector) git(ctx context.Context, root string, command ...string) (result, error) {
	if i == nil || i.executable == "" || i.run == nil {
		return result{}, repository.ErrGitUnavailable
	}
	commandCtx, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()
	args := []string{"--no-pager", "--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "color.ui=false", "-c", "core.quotepath=false", "-C", root}
	args = append(args, command...)
	result, err := i.run(commandCtx, i.executable, args, gitEnvironment())
	if errors.Is(err, errOutputLimit) {
		return result, repository.ErrOutputLimit
	}
	if commandCtx.Err() != nil {
		return result, fmt.Errorf("%w: Git command timed out or was canceled", repository.ErrInspectionFailed)
	}
	if err != nil && result.exitCode < 0 {
		return result, fmt.Errorf("%w: start Git: %v", repository.ErrInspectionFailed, err)
	}
	return result, nil
}

func canonicalDirectory(path string) (string, error) {
	if path == "" || strings.IndexByte(path, 0) >= 0 || len(path) > 32767 {
		return "", repository.ErrInvalidPath
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", repository.ErrInvalidPath
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return "", repository.ErrInvalidPath
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", repository.ErrInvalidPath
	}
	return filepath.Clean(resolved), nil
}

func containsPath(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func parseStatus(output []byte) ([]workspace.Change, error) {
	parts := bytes.Split(output, []byte{0})
	changes := make([]workspace.Change, 0, len(parts))
	for index := 0; index < len(parts); index++ {
		if len(parts[index]) == 0 {
			continue
		}
		record := string(parts[index])
		var change workspace.Change
		switch {
		case strings.HasPrefix(record, "1 "):
			fields := strings.SplitN(record, " ", 9)
			if len(fields) != 9 || len(fields[1]) != 2 {
				return nil, repository.ErrInspectionFailed
			}
			change = workspace.Change{Path: fields[8], Kind: workspace.ChangeTracked, IndexStatus: fields[1][0], WorktreeStatus: fields[1][1]}
		case strings.HasPrefix(record, "2 "):
			fields := strings.SplitN(record, " ", 10)
			if len(fields) != 10 || len(fields[1]) != 2 || index+1 >= len(parts) || len(parts[index+1]) == 0 {
				return nil, repository.ErrInspectionFailed
			}
			index++
			change = workspace.Change{Path: fields[9], OriginalPath: string(parts[index]), Kind: workspace.ChangeRenamed, IndexStatus: fields[1][0], WorktreeStatus: fields[1][1]}
		case strings.HasPrefix(record, "u "):
			fields := strings.SplitN(record, " ", 11)
			if len(fields) != 11 || len(fields[1]) != 2 {
				return nil, repository.ErrInspectionFailed
			}
			change = workspace.Change{Path: fields[10], Kind: workspace.ChangeTracked, IndexStatus: fields[1][0], WorktreeStatus: fields[1][1]}
		case strings.HasPrefix(record, "? "):
			change = workspace.Change{Path: strings.TrimPrefix(record, "? "), Kind: workspace.ChangeUntracked, IndexStatus: '?', WorktreeStatus: '?'}
		default:
			return nil, repository.ErrInspectionFailed
		}
		if err := change.Validate(); err != nil {
			return nil, repository.ErrInspectionFailed
		}
		changes = append(changes, change)
		if len(changes) > workspace.MaxChanges {
			return nil, repository.ErrOutputLimit
		}
	}
	sort.Slice(changes, func(left, right int) bool { return changes[left].Path < changes[right].Path })
	return changes, nil
}

var errOutputLimit = errors.New("command output limit exceeded")

type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(payload []byte) (int, error) {
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.exceeded = true
		return 0, errOutputLimit
	}
	if len(payload) > remaining {
		_, _ = b.buffer.Write(payload[:remaining])
		b.exceeded = true
		return remaining, errOutputLimit
	}
	return b.buffer.Write(payload)
}

func runCommand(ctx context.Context, executable string, args, environment []string) (result, error) {
	command := exec.CommandContext(ctx, executable, args...)
	command.Env = environment
	command.Stdin = nil
	stdout := &limitedBuffer{limit: maxStdoutBytes}
	stderr := &limitedBuffer{limit: maxStderrBytes}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	exitCode := 0
	if err != nil {
		exitCode = -1
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
		}
		if errors.Is(err, errOutputLimit) || stdout.exceeded || stderr.exceeded {
			return result{stdout: stdout.buffer.Bytes(), stderr: stderr.buffer.Bytes(), exitCode: exitCode}, errOutputLimit
		}
	}
	return result{stdout: append([]byte(nil), stdout.buffer.Bytes()...), stderr: append([]byte(nil), stderr.buffer.Bytes()...), exitCode: exitCode}, err
}

func gitEnvironment() []string {
	environment := []string{
		"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat", "LC_ALL=C",
	}
	globalConfig := "/dev/null"
	if runtime.GOOS == "windows" {
		globalConfig = "NUL"
	}
	environment = append(environment, "GIT_CONFIG_GLOBAL="+globalConfig)
	for _, name := range []string{"SystemRoot", "WINDIR", "USERPROFILE", "HOME", "TEMP", "TMP"} {
		if value := os.Getenv(name); value != "" {
			environment = append(environment, name+"="+value)
		}
	}
	return environment
}
