package gitcli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspace"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/worktree"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/repository"
)

const (
	maxSnapshotEntries  = 200_000
	maxDiffFiles        = 256
	maxDiffBytesPerFile = 64 << 10
	maxDiffBytesTotal   = 512 << 10
)

func (m *WorktreeManager) Review(ctx context.Context, record worktree.Record) (repository.WorktreeReview, error) {
	before, err := m.Snapshot(ctx, record)
	if err != nil {
		return repository.WorktreeReview{}, err
	}
	status, err := m.git(ctx, record.Root, "status", "--porcelain=v2", "-z", "--untracked-files=all")
	if err != nil || status.exitCode != 0 {
		return repository.WorktreeReview{}, repository.ErrWorktreeSnapshotFailed
	}
	changes, err := parseStatus(status.stdout)
	if err != nil {
		return repository.WorktreeReview{}, repository.ErrWorktreeSnapshotFailed
	}
	diffs, err := m.reviewDiffs(ctx, record, changes)
	if err != nil {
		return repository.WorktreeReview{}, err
	}
	after, err := m.Snapshot(ctx, record)
	if err != nil || before.Hash != after.Hash {
		return repository.WorktreeReview{}, repository.ErrWorktreeSnapshotFailed
	}
	for _, change := range changes {
		if err := change.Validate(); err != nil {
			return repository.WorktreeReview{}, repository.ErrWorktreeSnapshotFailed
		}
	}
	return repository.WorktreeReview{StateHash: after.Hash, PatchHash: patchHashForState(after.Hash), Changes: append([]workspace.Change(nil), changes...), Diffs: diffs}, nil
}

func (m *WorktreeManager) reviewDiffs(ctx context.Context, record worktree.Record, changes []workspace.Change) ([]repository.FileDiff, error) {
	diffs := make([]repository.FileDiff, 0, len(changes))
	remaining := maxDiffBytesTotal
	stats, err := m.reviewNumstats(ctx, record.Root, changes)
	if err != nil {
		return nil, err
	}
	for index, change := range changes {
		if index >= maxDiffFiles {
			diffs = append(diffs, repository.FileDiff{Path: change.Path, OmittedReason: "file_limit"})
			continue
		}
		var diff repository.FileDiff
		var err error
		if change.Kind == workspace.ChangeUntracked {
			diff, err = reviewUntrackedDiff(record.Root, change.Path)
		} else {
			diff, err = m.reviewTrackedDiff(ctx, record.Root, change, stats)
		}
		if err != nil {
			return nil, err
		}
		if len(diff.Text) > remaining {
			diff.Text = truncateUTF8(diff.Text, remaining)
			diff.Truncated = true
		}
		remaining -= len(diff.Text)
		if remaining < 0 {
			remaining = 0
		}
		diffs = append(diffs, diff)
	}
	return diffs, nil
}

func (m *WorktreeManager) reviewNumstats(ctx context.Context, root string, changes []workspace.Change) (map[string]repository.FileDiff, error) {
	args := []string{"diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", "HEAD", "--"}
	for index, change := range changes {
		if index >= maxDiffFiles {
			break
		}
		if change.Kind == workspace.ChangeUntracked {
			continue
		}
		args = append(args, change.Path)
		if change.OriginalPath != "" {
			args = append(args, change.OriginalPath)
		}
	}
	stats := make(map[string]repository.FileDiff)
	if len(args) == 8 {
		return stats, nil
	}
	output, err := m.git(ctx, root, args...)
	if err != nil || output.exitCode != 0 {
		return nil, repository.ErrWorktreeSnapshotFailed
	}
	for _, entry := range bytes.Split(output.stdout, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		fields := bytes.SplitN(entry, []byte{'\t'}, 3)
		if len(fields) != 3 {
			return nil, repository.ErrWorktreeSnapshotFailed
		}
		added, deleted, binary := parseNumstat(entry)
		stats[string(fields[2])] = repository.FileDiff{AddedLines: added, DeletedLines: deleted, Binary: binary}
	}
	return stats, nil
}

func (m *WorktreeManager) reviewTrackedDiff(ctx context.Context, root string, change workspace.Change, stats map[string]repository.FileDiff) (repository.FileDiff, error) {
	paths := []string{change.Path}
	if change.OriginalPath != "" {
		paths = append(paths, change.OriginalPath)
	}
	result := repository.FileDiff{Path: change.Path}
	for _, path := range paths {
		stat := stats[path]
		result.AddedLines += stat.AddedLines
		result.DeletedLines += stat.DeletedLines
		result.Binary = result.Binary || stat.Binary
	}
	if result.Binary {
		result.OmittedReason = "binary"
		return result, nil
	}
	diffArgs := []string{"diff", "--no-ext-diff", "--no-textconv", "--unified=3", "--src-prefix=a/", "--dst-prefix=b/", "HEAD", "--"}
	diff, diffErr := m.git(ctx, root, append(diffArgs, paths...)...)
	if diff.exitCode != 0 && !errors.Is(diffErr, repository.ErrOutputLimit) {
		return repository.FileDiff{}, repository.ErrWorktreeSnapshotFailed
	}
	if diffErr != nil && !errors.Is(diffErr, repository.ErrOutputLimit) {
		return repository.FileDiff{}, repository.ErrWorktreeSnapshotFailed
	}
	if !utf8.Valid(diff.stdout) || bytes.IndexByte(diff.stdout, 0) >= 0 {
		result.Binary, result.OmittedReason = true, "binary"
		return result, nil
	}
	result.Text = truncateUTF8(string(diff.stdout), maxDiffBytesPerFile)
	result.Truncated = errors.Is(diffErr, repository.ErrOutputLimit) || len(diff.stdout) > len(result.Text)
	return result, nil
}

func reviewUntrackedDiff(root, gitPath string) (repository.FileDiff, error) {
	result := repository.FileDiff{Path: gitPath}
	if gitPath == "" || filepath.IsAbs(gitPath) || filepath.VolumeName(gitPath) != "" {
		return repository.FileDiff{}, repository.ErrWorktreeSnapshotFailed
	}
	path := filepath.Join(root, filepath.Clean(filepath.FromSlash(gitPath)))
	if !containsPath(root, path) {
		return repository.FileDiff{}, repository.ErrWorktreeSnapshotFailed
	}
	info, err := os.Lstat(path)
	if err != nil {
		return repository.FileDiff{}, repository.ErrWorktreeSnapshotFailed
	}
	if info.Mode()&os.ModeSymlink != 0 {
		result.OmittedReason = "symlink"
		return result, nil
	}
	if !info.Mode().IsRegular() {
		result.OmittedReason = "unsupported_type"
		return result, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return repository.FileDiff{}, repository.ErrWorktreeSnapshotFailed
	}
	payload, readErr := io.ReadAll(io.LimitReader(file, maxDiffBytesPerFile+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return repository.FileDiff{}, repository.ErrWorktreeSnapshotFailed
	}
	if bytes.IndexByte(payload, 0) >= 0 || !utf8.Valid(payload) {
		result.Binary, result.OmittedReason = true, "binary"
		return result, nil
	}
	truncated := len(payload) > maxDiffBytesPerFile
	if truncated {
		payload = payload[:maxDiffBytesPerFile]
		for !utf8.Valid(payload) && len(payload) > 0 {
			payload = payload[:len(payload)-1]
		}
	}
	content := strings.ReplaceAll(string(payload), "\r\n", "\n")
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	if content == "" {
		lines = nil
	}
	var builder strings.Builder
	builder.WriteString("diff --git a/")
	builder.WriteString(gitPath)
	builder.WriteString(" b/")
	builder.WriteString(gitPath)
	builder.WriteString("\nnew file mode 100644\n--- /dev/null\n+++ b/")
	builder.WriteString(gitPath)
	builder.WriteString("\n@@ -0,0 +1,")
	builder.WriteString(strconv.Itoa(len(lines)))
	builder.WriteString(" @@\n")
	for _, line := range lines {
		builder.WriteByte('+')
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	result.Text = truncateUTF8(builder.String(), maxDiffBytesPerFile)
	result.Truncated = truncated || len(result.Text) < builder.Len()
	result.AddedLines = len(lines)
	return result, nil
}

func parseNumstat(payload []byte) (int, int, bool) {
	for _, line := range strings.Split(strings.TrimSpace(string(payload)), "\n") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 2 {
			continue
		}
		if fields[0] == "-" || fields[1] == "-" {
			return 0, 0, true
		}
		added, addErr := strconv.Atoi(fields[0])
		deleted, deleteErr := strconv.Atoi(fields[1])
		if addErr == nil && deleteErr == nil {
			return added, deleted, false
		}
	}
	return 0, 0, false
}

func truncateUTF8(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	payload := []byte(value[:limit])
	for !utf8.Valid(payload) && len(payload) > 0 {
		payload = payload[:len(payload)-1]
	}
	return string(payload)
}

func (m *WorktreeManager) Snapshot(ctx context.Context, record worktree.Record) (repository.WorktreeState, error) {
	if err := record.Validate(); err != nil {
		return repository.WorktreeState{}, repository.ErrWorktreeSnapshotFailed
	}
	status, err := m.git(ctx, record.Root, "status", "--porcelain=v2", "-z", "--untracked-files=all")
	if err != nil || status.exitCode != 0 {
		return repository.WorktreeState{}, repository.ErrWorktreeSnapshotFailed
	}
	changes, err := parseStatus(status.stdout)
	if err != nil || len(changes) > maxSnapshotEntries {
		return repository.WorktreeState{}, repository.ErrWorktreeSnapshotFailed
	}
	hash := sha256.New()
	_, _ = io.WriteString(hash, "talos.worktree-state/2\x00"+record.BaselineCommit+"\x00")
	_, _ = hash.Write(status.stdout)
	for _, change := range changes {
		if err := ctx.Err(); err != nil {
			return repository.WorktreeState{}, repository.ErrWorktreeSnapshotFailed
		}
		kind, size, contentHash, err := m.snapshotPath(ctx, record.Root, change.Path)
		if err != nil {
			return repository.WorktreeState{}, err
		}
		_, _ = io.WriteString(hash, change.Path+"\x00"+kind+"\x00"+strconv.FormatInt(size, 10)+"\x00"+contentHash+"\x00")
	}
	return repository.WorktreeState{Hash: hex.EncodeToString(hash.Sum(nil))}, nil
}

func patchHashForState(stateHash string) string {
	patchHash := sha256.Sum256([]byte("talos.patch-review/1\x00" + stateHash))
	return hex.EncodeToString(patchHash[:])
}

func snapshotPath(ctx context.Context, root, gitPath string) (string, int64, string, error) {
	if gitPath == "" || strings.IndexByte(gitPath, 0) >= 0 || filepath.IsAbs(gitPath) || filepath.VolumeName(gitPath) != "" {
		return "", 0, "", repository.ErrWorktreeSnapshotFailed
	}
	relative := filepath.Clean(filepath.FromSlash(gitPath))
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", 0, "", repository.ErrWorktreeSnapshotFailed
	}
	path := filepath.Join(root, relative)
	if !containsPath(root, path) {
		return "", 0, "", repository.ErrWorktreeSnapshotFailed
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "missing", 0, strings.Repeat("0", 64), nil
	}
	if err != nil {
		return "", 0, "", repository.ErrWorktreeSnapshotFailed
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return "", 0, "", repository.ErrWorktreeSnapshotFailed
		}
		sum := sha256.Sum256([]byte(target))
		return "symlink", int64(len(target)), hex.EncodeToString(sum[:]), nil
	}
	if !info.Mode().IsRegular() {
		return "", 0, "", repository.ErrWorktreeSnapshotFailed
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, "", repository.ErrWorktreeSnapshotFailed
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return "", 0, "", repository.ErrWorktreeSnapshotFailed
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, &contextReader{ctx: ctx, reader: file}); err != nil {
		return "", 0, "", repository.ErrWorktreeSnapshotFailed
	}
	finished, err := file.Stat()
	if err != nil || !os.SameFile(opened, finished) || opened.Size() != finished.Size() || opened.Mode() != finished.Mode() || !opened.ModTime().Equal(finished.ModTime()) {
		return "", 0, "", repository.ErrWorktreeSnapshotFailed
	}
	return fmt.Sprintf("file:%o", opened.Mode().Perm()), opened.Size(), hex.EncodeToString(hash.Sum(nil)), nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(payload []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(payload)
}
