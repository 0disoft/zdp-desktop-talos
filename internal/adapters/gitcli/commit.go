package gitcli

import (
	"context"
	"regexp"

	"github.com/0disoft/zdp-desktop-talos/internal/ports/repository"
)

var commitObjectPattern = regexp.MustCompile(`^[0-9a-f]{40,64}$`)

func (i *Inspector) ContainsCommit(ctx context.Context, requestedPath, commit string) (bool, error) {
	if ctx == nil || !commitObjectPattern.MatchString(commit) {
		return false, repository.ErrInvalidCommit
	}
	root, err := canonicalDirectory(requestedPath)
	if err != nil {
		return false, err
	}
	result, err := i.git(ctx, root, "cat-file", "-e", commit+"^{commit}")
	if err != nil {
		return false, err
	}
	switch result.exitCode {
	case 0:
		return true, nil
	case 1, 128:
		return false, nil
	default:
		return false, repository.ErrInspectionFailed
	}
}
