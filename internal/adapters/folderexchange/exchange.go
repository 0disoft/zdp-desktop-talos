package folderexchange

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
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/0disoft/zdp-desktop-talos/internal/ports/syncexchange"
)

var packNamePattern = regexp.MustCompile(`^(\d{20})-(\d{20})-([a-f0-9]{64})\.talos-pack$`)

type Exchange struct{}

func New() *Exchange { return &Exchange{} }

func (e *Exchange) WritePack(ctx context.Context, root string, descriptor syncexchange.PackDescriptor, encoded []byte) (syncexchange.StoredPack, bool, error) {
	if ctx == nil || e == nil || !validDescriptor(descriptor) || len(encoded) == 0 || len(encoded) > syncexchange.MaxPackBytes {
		return syncexchange.StoredPack{}, false, syncexchange.ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return syncexchange.StoredPack{}, false, err
	}
	resolvedRoot, err := safeRoot(root)
	if err != nil {
		return syncexchange.StoredPack{}, false, err
	}
	directory, err := packDirectory(resolvedRoot, descriptor.VaultID, descriptor.DeviceID, descriptor.CreatedAt.UTC().Format("2006"), descriptor.CreatedAt.UTC().Format("01"))
	if err != nil {
		return syncexchange.StoredPack{}, false, err
	}
	if err := createSafeDirectoryTree(resolvedRoot, directory); err != nil {
		return syncexchange.StoredPack{}, false, err
	}
	name := fmt.Sprintf("%020d-%020d-%s.talos-pack", descriptor.SequenceStart, descriptor.SequenceEnd, digest(descriptor.PackID))
	destination := filepath.Join(directory, name)
	relative, err := safeRelative(resolvedRoot, destination)
	if err != nil {
		return syncexchange.StoredPack{}, false, err
	}
	stored := syncexchange.StoredPack{RelativePath: filepath.ToSlash(relative), SequenceStart: descriptor.SequenceStart, SequenceEnd: descriptor.SequenceEnd}
	if replay, found, err := compareExisting(destination, encoded); err != nil {
		return syncexchange.StoredPack{}, false, err
	} else if found {
		if replay {
			return stored, true, nil
		}
		return syncexchange.StoredPack{}, false, syncexchange.ErrPackConflict
	}

	temporary, err := os.CreateTemp(directory, ".talos-pack-*.tmp")
	if err != nil {
		return syncexchange.StoredPack{}, false, fmt.Errorf("create sync pack staging file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return syncexchange.StoredPack{}, false, fmt.Errorf("protect sync pack staging file: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return syncexchange.StoredPack{}, false, fmt.Errorf("write sync pack staging file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return syncexchange.StoredPack{}, false, fmt.Errorf("flush sync pack staging file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return syncexchange.StoredPack{}, false, fmt.Errorf("close sync pack staging file: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return syncexchange.StoredPack{}, false, err
	}
	if err := os.Link(temporaryPath, destination); err != nil {
		if replay, found, compareErr := compareExisting(destination, encoded); compareErr != nil {
			return syncexchange.StoredPack{}, false, compareErr
		} else if found {
			if replay {
				return stored, true, nil
			}
			return syncexchange.StoredPack{}, false, syncexchange.ErrPackConflict
		}
		return syncexchange.StoredPack{}, false, fmt.Errorf("publish sync pack atomically: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		return syncexchange.StoredPack{}, false, fmt.Errorf("remove published sync pack staging link: %w", err)
	}
	return stored, false, nil
}

func (e *Exchange) ReadPacks(ctx context.Context, root, vaultID, deviceID string) ([]syncexchange.ReadPack, error) {
	if ctx == nil || e == nil || strings.TrimSpace(vaultID) == "" || strings.TrimSpace(deviceID) == "" {
		return nil, syncexchange.ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resolvedRoot, err := existingSafeRoot(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	base := filepath.Join(resolvedRoot, "vaults", hashSegment("vault", vaultID), "packs", hashSegment("device", deviceID))
	if _, err := safeRelative(resolvedRoot, base); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(base); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("inspect sync pack device directory: %w", err)
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, syncexchange.ErrUnsafePath
	}

	type candidate struct {
		path     string
		relative string
		start    uint64
		end      uint64
	}
	var candidates []candidate
	err = filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == base {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return syncexchange.ErrUnsafePath
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Ext(entry.Name()) != ".talos-pack" {
			return nil
		}
		matches := packNamePattern.FindStringSubmatch(entry.Name())
		if len(matches) != 4 {
			return syncexchange.ErrUnsafePath
		}
		start, startErr := strconv.ParseUint(matches[1], 10, 64)
		end, endErr := strconv.ParseUint(matches[2], 10, 64)
		if startErr != nil || endErr != nil || start == 0 || end < start {
			return syncexchange.ErrUnsafePath
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > syncexchange.MaxPackBytes {
			return syncexchange.ErrReadLimit
		}
		relative, err := safeRelative(resolvedRoot, path)
		if err != nil {
			return err
		}
		candidates = append(candidates, candidate{path: path, relative: filepath.ToSlash(relative), start: start, end: end})
		if len(candidates) > syncexchange.MaxPacksPerRead {
			return syncexchange.ErrReadLimit
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan sync pack folder: %w", err)
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].start != candidates[right].start {
			return candidates[left].start < candidates[right].start
		}
		if candidates[left].end != candidates[right].end {
			return candidates[left].end < candidates[right].end
		}
		return candidates[left].relative < candidates[right].relative
	})
	result := make([]syncexchange.ReadPack, 0, len(candidates))
	total := 0
	for _, candidate := range candidates {
		encoded, err := readBounded(candidate.path)
		if err != nil {
			clearReadPacks(result)
			return nil, err
		}
		total += len(encoded)
		if total > syncexchange.MaxTotalReadBytes {
			clear(encoded)
			clearReadPacks(result)
			return nil, syncexchange.ErrReadLimit
		}
		result = append(result, syncexchange.ReadPack{StoredPack: syncexchange.StoredPack{RelativePath: candidate.relative, SequenceStart: candidate.start, SequenceEnd: candidate.end}, Encoded: encoded})
	}
	return result, nil
}

func validDescriptor(value syncexchange.PackDescriptor) bool {
	return strings.TrimSpace(value.VaultID) != "" && strings.TrimSpace(value.DeviceID) != "" && strings.TrimSpace(value.PackID) != "" && value.SequenceStart > 0 && value.SequenceEnd >= value.SequenceStart && !value.CreatedAt.IsZero()
}

func safeRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", syncexchange.ErrInvalidRequest
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", syncexchange.ErrUnsafePath
	}
	if info, inspectErr := os.Lstat(absolute); inspectErr == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", syncexchange.ErrUnsafePath
		}
		return existingSafeRoot(absolute)
	} else if !errors.Is(inspectErr, os.ErrNotExist) {
		return "", fmt.Errorf("inspect sync exchange root: %w", inspectErr)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return "", fmt.Errorf("create sync exchange root: %w", err)
	}
	return existingSafeRoot(absolute)
}

func existingSafeRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", syncexchange.ErrInvalidRequest
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", syncexchange.ErrUnsafePath
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", syncexchange.ErrUnsafePath
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", syncexchange.ErrUnsafePath
	}
	return filepath.Clean(resolved), nil
}

func packDirectory(root, vaultID, deviceID, year, month string) (string, error) {
	if len(year) != 4 || len(month) != 2 {
		return "", syncexchange.ErrInvalidRequest
	}
	directory := filepath.Join(root, "vaults", hashSegment("vault", vaultID), "packs", hashSegment("device", deviceID), year, month)
	if _, err := safeRelative(root, directory); err != nil {
		return "", err
	}
	return directory, nil
}

func createSafeDirectoryTree(root, destination string) error {
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return fmt.Errorf("create sync pack directory: %w", err)
	}
	relative, err := safeRelative(root, destination)
	if err != nil {
		return err
	}
	current := root
	for _, component := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
		if component == "." || component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return syncexchange.ErrUnsafePath
		}
	}
	return nil
}

func safeRelative(root, path string) (string, error) {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", syncexchange.ErrUnsafePath
	}
	return relative, nil
}

func compareExisting(path string, expected []byte) (bool, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("inspect existing sync pack: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > syncexchange.MaxPackBytes {
		return false, true, syncexchange.ErrUnsafePath
	}
	encoded, err := readBounded(path)
	if err != nil {
		return false, true, err
	}
	defer clear(encoded)
	return bytes.Equal(encoded, expected), true, nil
}

func readBounded(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open sync pack: %w", err)
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, syncexchange.MaxPackBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read sync pack: %w", err)
	}
	if len(encoded) == 0 || len(encoded) > syncexchange.MaxPackBytes {
		clear(encoded)
		return nil, syncexchange.ErrReadLimit
	}
	return encoded, nil
}

func hashSegment(prefix, value string) string { return prefix + "-" + digest(value) }

func digest(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func clearReadPacks(packs []syncexchange.ReadPack) {
	for index := range packs {
		clear(packs[index].Encoded)
	}
}
