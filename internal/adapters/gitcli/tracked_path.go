package gitcli

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/0disoft/zdp-desktop-talos/internal/ports/repository"
)

func (i *Inspector) IsTracked(ctx context.Context, root, repositoryRelativePath string) (bool, error) {
	if ctx == nil || !validTrackedPath(repositoryRelativePath) {
		return false, repository.ErrInvalidPath
	}
	result, err := i.git(ctx, root, "ls-files", "--error-unmatch", "--", repositoryRelativePath)
	if err != nil && result.exitCode < 0 {
		return false, err
	}
	switch result.exitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, repository.ErrInspectionFailed
	}
}

func validTrackedPath(value string) bool {
	if value == "" || len(value) > 4096 || strings.IndexByte(value, 0) >= 0 || strings.Contains(value, `\`) || filepath.IsAbs(value) || filepath.VolumeName(value) != "" {
		return false
	}
	clean := filepath.Clean(filepath.FromSlash(value))
	return clean != "." && clean != ".." && !filepath.IsAbs(clean) && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}
