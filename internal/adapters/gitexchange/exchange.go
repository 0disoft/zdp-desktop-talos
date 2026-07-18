package gitexchange

import (
	"context"
	"errors"
	"path"
	"path/filepath"
	"strings"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspace"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/repository"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/syncexchange"
)

const DataDirectory = "talos-sync"

var (
	ErrInvalidRequest    = errors.New("invalid Git sync exchange request")
	ErrUnavailable       = errors.New("Git sync exchange is unavailable")
	ErrRepositoryDirty   = errors.New("Git sync exchange repository is dirty")
	ErrDetachedHead      = errors.New("Git sync exchange repository has detached HEAD")
	ErrRepositoryChanged = errors.New("Git sync exchange repository changed during operation")
	ErrPackUntracked     = errors.New("Git sync exchange pack is ignored or untracked")
)

type Exchange struct {
	inspector repository.Inspector
	tracker   repository.TrackedPathVerifier
	files     syncexchange.Exchange
}

func New(inspector repository.Inspector, tracker repository.TrackedPathVerifier, files syncexchange.Exchange) (*Exchange, error) {
	if inspector == nil || tracker == nil || files == nil {
		return nil, ErrInvalidRequest
	}
	return &Exchange{inspector: inspector, tracker: tracker, files: files}, nil
}

func (e *Exchange) WritePack(ctx context.Context, root string, descriptor syncexchange.PackDescriptor, encoded []byte) (syncexchange.StoredPack, bool, error) {
	before, err := e.cleanSnapshot(ctx, root)
	if err != nil {
		return syncexchange.StoredPack{}, false, err
	}
	stored, replay, err := e.files.WritePack(ctx, filepath.Join(before.Root, DataDirectory), descriptor, encoded)
	if err != nil {
		return syncexchange.StoredPack{}, false, err
	}
	repositoryPath := path.Join(DataDirectory, stored.RelativePath)
	after, err := e.inspector.Inspect(ctx, before.Root)
	if err != nil || !sameRevision(before, after) {
		return syncexchange.StoredPack{}, false, ErrRepositoryChanged
	}
	if !after.Dirty {
		tracked, err := e.tracker.IsTracked(ctx, before.Root, repositoryPath)
		if err != nil {
			return syncexchange.StoredPack{}, false, err
		}
		if !tracked {
			return syncexchange.StoredPack{}, false, ErrPackUntracked
		}
		if !replay {
			return syncexchange.StoredPack{}, false, ErrRepositoryChanged
		}
	} else if !onlyPreparedPackChange(after.Changes, repositoryPath) {
		return syncexchange.StoredPack{}, false, ErrRepositoryChanged
	}
	stored.RelativePath = repositoryPath
	return stored, replay, nil
}

func (e *Exchange) ReadPacks(ctx context.Context, root, vaultID, deviceID string) ([]syncexchange.ReadPack, error) {
	before, err := e.cleanSnapshot(ctx, root)
	if err != nil {
		return nil, err
	}
	packs, err := e.files.ReadPacks(ctx, filepath.Join(before.Root, DataDirectory), vaultID, deviceID)
	if err != nil {
		return nil, err
	}
	clearOnError := true
	defer func() {
		if clearOnError {
			clearPacks(packs)
		}
	}()
	for index := range packs {
		repositoryPath := path.Join(DataDirectory, packs[index].RelativePath)
		tracked, err := e.tracker.IsTracked(ctx, before.Root, repositoryPath)
		if err != nil {
			return nil, err
		}
		if !tracked {
			return nil, ErrPackUntracked
		}
		packs[index].RelativePath = repositoryPath
	}
	after, err := e.inspector.Inspect(ctx, before.Root)
	if err != nil || after.Dirty || !sameRevision(before, after) {
		return nil, ErrRepositoryChanged
	}
	clearOnError = false
	return packs, nil
}

func (e *Exchange) cleanSnapshot(ctx context.Context, root string) (workspace.RepositorySnapshot, error) {
	if e == nil || e.inspector == nil || e.tracker == nil || e.files == nil || ctx == nil {
		return workspace.RepositorySnapshot{}, ErrInvalidRequest
	}
	resolved, err := canonicalRoot(root)
	if err != nil {
		return workspace.RepositorySnapshot{}, err
	}
	snapshot, err := e.inspector.Inspect(ctx, resolved)
	if err != nil {
		if errors.Is(err, repository.ErrInvalidPath) || errors.Is(err, repository.ErrNotRepository) || errors.Is(err, repository.ErrNoBaselineCommit) {
			return workspace.RepositorySnapshot{}, ErrInvalidRequest
		}
		return workspace.RepositorySnapshot{}, err
	}
	if filepath.Clean(snapshot.Root) != resolved {
		return workspace.RepositorySnapshot{}, ErrInvalidRequest
	}
	if snapshot.Detached || snapshot.HeadRef == "" {
		return workspace.RepositorySnapshot{}, ErrDetachedHead
	}
	if snapshot.Dirty {
		return workspace.RepositorySnapshot{}, ErrRepositoryDirty
	}
	return snapshot, nil
}

func canonicalRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" || strings.IndexByte(root, 0) >= 0 || len(root) > 32767 || !filepath.IsAbs(root) {
		return "", ErrInvalidRequest
	}
	absolute := filepath.Clean(root)
	info, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", ErrInvalidRequest
	}
	resolved, err := filepath.Abs(info)
	if err != nil {
		return "", ErrInvalidRequest
	}
	return filepath.Clean(resolved), nil
}

func sameRevision(left, right workspace.RepositorySnapshot) bool {
	return filepath.Clean(left.Root) == filepath.Clean(right.Root) && left.BaselineCommit == right.BaselineCommit && left.HeadRef == right.HeadRef && !right.Detached
}

func onlyPreparedPackChange(changes []workspace.Change, expectedPath string) bool {
	if len(changes) != 1 || changes[0].Kind != workspace.ChangeUntracked || changes[0].OriginalPath != "" {
		return false
	}
	actual := filepath.ToSlash(filepath.Clean(filepath.FromSlash(changes[0].Path)))
	return actual == expectedPath
}

func clearPacks(packs []syncexchange.ReadPack) {
	for index := range packs {
		clear(packs[index].Encoded)
	}
}
