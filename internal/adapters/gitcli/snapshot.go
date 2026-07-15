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
	"sort"
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

type snapshotEntry struct {
	path      string
	indexMeta string
}

func (m *WorktreeManager) Review(ctx context.Context, record worktree.Record) (repository.WorktreeReview, error) {
	before, err := m.Snapshot(ctx, record)
	if err != nil {
		return repository.WorktreeReview{}, err
	}
	status, err := m.git(ctx, record.Root, "status", "--porcelain=v2", "-z", "--untracked-files=normal")
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
	patchHash := sha256.Sum256([]byte("talos.patch-review/1\x00" + after.Hash))
	return repository.WorktreeReview{StateHash: after.Hash, PatchHash: hex.EncodeToString(patchHash[:]), Changes: append([]workspace.Change(nil), changes...), Diffs: diffs}, nil
}

func (m *WorktreeManager) reviewDiffs(ctx context.Context, record worktree.Record, changes []workspace.Change) ([]repository.FileDiff, error) {
	diffs := make([]repository.FileDiff, 0, len(changes))
	remaining := maxDiffBytesTotal
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
			diff, err = m.reviewTrackedDiff(ctx, record.Root, change)
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

func (m *WorktreeManager) reviewTrackedDiff(ctx context.Context, root string, change workspace.Change) (repository.FileDiff, error) {
	paths := []string{change.Path}
	if change.OriginalPath != "" {
		paths = append(paths, change.OriginalPath)
	}
	numstatArgs := []string{"diff", "--numstat", "--no-renames", "HEAD", "--"}
	numstat, err := m.git(ctx, root, append(numstatArgs, paths...)...)
	if err != nil || numstat.exitCode != 0 {
		return repository.FileDiff{}, repository.ErrWorktreeSnapshotFailed
	}
	added, deleted, binary := parseNumstat(numstat.stdout)
	result := repository.FileDiff{Path: change.Path, Binary: binary, AddedLines: added, DeletedLines: deleted}
	if binary {
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
	tracked, err := m.git(ctx, record.Root, "ls-files", "--stage", "-z", "--cached")
	if err != nil || tracked.exitCode != 0 {
		return repository.WorktreeState{}, repository.ErrWorktreeSnapshotFailed
	}
	untracked, err := m.git(ctx, record.Root, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil || untracked.exitCode != 0 {
		return repository.WorktreeState{}, repository.ErrWorktreeSnapshotFailed
	}
	entries, err := parseSnapshotEntries(tracked.stdout, untracked.stdout)
	if err != nil {
		return repository.WorktreeState{}, err
	}
	hash := sha256.New()
	_, _ = io.WriteString(hash, "talos.worktree-state/1\x00"+record.BaselineCommit+"\x00")
	for _, entry := range entries {
		kind, size, contentHash, err := snapshotPath(record.Root, entry.path)
		if err != nil {
			return repository.WorktreeState{}, err
		}
		_, _ = io.WriteString(hash, entry.path+"\x00"+entry.indexMeta+"\x00"+kind+"\x00"+strconv.FormatInt(size, 10)+"\x00"+contentHash+"\x00")
	}
	return repository.WorktreeState{Hash: hex.EncodeToString(hash.Sum(nil))}, nil
}

func parseSnapshotEntries(tracked, untracked []byte) ([]snapshotEntry, error) {
	entries := make([]snapshotEntry, 0)
	for _, item := range splitNUL(tracked) {
		tab := strings.IndexByte(item, '\t')
		if tab <= 0 || tab == len(item)-1 {
			return nil, repository.ErrWorktreeSnapshotFailed
		}
		entries = append(entries, snapshotEntry{path: item[tab+1:], indexMeta: item[:tab]})
	}
	for _, path := range splitNUL(untracked) {
		entries = append(entries, snapshotEntry{path: path, indexMeta: "untracked"})
	}
	if len(entries) > maxSnapshotEntries {
		return nil, repository.ErrWorktreeSnapshotFailed
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].path == entries[j].path {
			return entries[i].indexMeta < entries[j].indexMeta
		}
		return entries[i].path < entries[j].path
	})
	return entries, nil
}

func splitNUL(payload []byte) []string {
	parts := strings.Split(string(payload), "\x00")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func snapshotPath(root, gitPath string) (string, int64, string, error) {
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
	if _, err := io.Copy(hash, file); err != nil {
		return "", 0, "", repository.ErrWorktreeSnapshotFailed
	}
	finished, err := file.Stat()
	if err != nil || !os.SameFile(opened, finished) || opened.Size() != finished.Size() || opened.Mode() != finished.Mode() || !opened.ModTime().Equal(finished.ModTime()) {
		return "", 0, "", repository.ErrWorktreeSnapshotFailed
	}
	return fmt.Sprintf("file:%o", opened.Mode().Perm()), opened.Size(), hex.EncodeToString(hash.Sum(nil)), nil
}
