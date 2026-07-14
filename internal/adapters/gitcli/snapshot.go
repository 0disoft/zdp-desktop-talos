package gitcli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspace"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/worktree"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/repository"
)

const maxSnapshotEntries = 200_000

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
	after, err := m.Snapshot(ctx, record)
	if err != nil || before.Hash != after.Hash {
		return repository.WorktreeReview{}, repository.ErrWorktreeSnapshotFailed
	}
	for _, change := range changes {
		if err := change.Validate(); err != nil {
			return repository.WorktreeReview{}, repository.ErrWorktreeSnapshotFailed
		}
	}
	return repository.WorktreeReview{StateHash: after.Hash, Changes: append([]workspace.Change(nil), changes...)}, nil
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
