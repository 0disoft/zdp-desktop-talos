package patchcommand

import (
	"github.com/0disoft/zdp-desktop-talos/internal/application/patchreview"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/patchaction"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
)

func Decide(record task.Record, contract task.ContractRevision, review patchreview.Result, request Request) error {
	if record.Status != task.StatusContracted || record.CurrentRevision != request.ExpectedRevision || contract.Revision != request.ExpectedRevision || review.ContractRevision != request.ExpectedRevision || review.PatchHash != request.ExpectedPatchHash || review.TaskID != record.ID || contract.TaskID != record.ID || contract.BaselineCommit != record.BaselineCommit || review.BaselineCommit != record.BaselineCommit {
		return ErrStaleReview
	}
	if request.Kind == patchaction.KindDiscard {
		return nil
	}
	if review.Status != patchreview.StatusFresh || review.Evidence == nil || review.Evidence.ContractRevision != request.ExpectedRevision || review.Evidence.WorktreeStateHash != review.StateHash {
		return ErrStaleReview
	}
	if review.SecretFindings != 0 {
		return ErrSecretFindings
	}
	if len(review.Changes) == 0 {
		return ErrNoChanges
	}
	if !fullyScanned(review) {
		return ErrUnscannableChanges
	}
	for _, change := range review.Changes {
		if !contract.AllowsPath(change.Path) || (change.OriginalPath != "" && !contract.AllowsPath(change.OriginalPath)) {
			return ErrScopeViolation
		}
	}
	return nil
}

func fullyScanned(review patchreview.Result) bool {
	if len(review.Diffs) != len(review.Changes) {
		return false
	}
	changes := make(map[string]struct{}, len(review.Changes))
	for _, change := range review.Changes {
		if change.Path == "" {
			return false
		}
		if _, exists := changes[change.Path]; exists {
			return false
		}
		changes[change.Path] = struct{}{}
	}
	seen := make(map[string]struct{}, len(review.Diffs))
	for _, diff := range review.Diffs {
		if diff.Path == "" || diff.Binary || diff.Truncated || diff.OmittedReason != "" {
			return false
		}
		if _, exists := changes[diff.Path]; !exists {
			return false
		}
		if _, exists := seen[diff.Path]; exists {
			return false
		}
		seen[diff.Path] = struct{}{}
	}
	return len(seen) == len(changes)
}
