package verification

import (
	"errors"
	"regexp"
	"time"
)

var (
	ErrInvalidEvidence = errors.New("invalid verification evidence")
	commitPattern      = regexp.MustCompile(`^[0-9a-f]{40,64}$`)
	hashPattern        = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Evidence struct {
	ID                string
	VaultID           string
	TaskID            string
	RunID             string
	AttemptID         string
	ContractRevision  int
	CommandIndex      int
	BaselineCommit    string
	WorktreeStateHash string
	CapabilityHash    string
	ExitCode          int
	StartedAt         time.Time
	FinishedAt        time.Time
	EventID           string
}

func (e Evidence) Validate() error {
	if e.ID == "" || e.VaultID == "" || e.TaskID == "" || e.RunID == "" || e.AttemptID == "" || e.EventID == "" {
		return ErrInvalidEvidence
	}
	if e.ContractRevision < 1 || e.CommandIndex < 0 || e.ExitCode != 0 {
		return ErrInvalidEvidence
	}
	if !commitPattern.MatchString(e.BaselineCommit) || !hashPattern.MatchString(e.WorktreeStateHash) || !hashPattern.MatchString(e.CapabilityHash) {
		return ErrInvalidEvidence
	}
	if e.StartedAt.IsZero() || e.FinishedAt.Before(e.StartedAt) {
		return ErrInvalidEvidence
	}
	return nil
}
