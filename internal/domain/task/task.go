package task

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	MaxGoalLength       = 4096
	MaxScopeEntries     = 256
	MaxAcceptanceItems  = 128
	MaxContractTextSize = 4096
)

var (
	ErrInvalidRecord = errors.New("invalid task record")
	commitPattern    = regexp.MustCompile(`^[0-9a-f]{40,64}$`)
)

type Status string

const StatusContracted Status = "contracted"

type Risk string

const (
	RiskLow    Risk = "low"
	RiskMedium Risk = "medium"
	RiskHigh   Risk = "high"
)

type Record struct {
	ID              string
	VaultID         string
	WorkspaceRoot   string
	BaselineCommit  string
	Status          Status
	CurrentRevision int
	CreatedAt       time.Time
	UpdatedAt       time.Time
	LastEventID     string
}

type ContractRevision struct {
	TaskID             string
	Revision           int
	BaselineCommit     string
	Goal               string
	AllowedPaths       []string
	ForbiddenActions   []string
	AcceptanceCriteria []string
	Risk               Risk
	CreatedAt          time.Time
	EventID            string
}

func (r Record) Validate() error {
	if r.ID == "" || r.VaultID == "" || !filepath.IsAbs(r.WorkspaceRoot) || !commitPattern.MatchString(r.BaselineCommit) {
		return fmt.Errorf("%w: identity, workspace root, and baseline are required", ErrInvalidRecord)
	}
	if r.Status != StatusContracted || r.CurrentRevision < 1 || r.CreatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) || r.LastEventID == "" {
		return fmt.Errorf("%w: lifecycle metadata is invalid", ErrInvalidRecord)
	}
	return nil
}

func (c ContractRevision) Validate() error {
	if c.TaskID == "" || c.Revision < 1 || !commitPattern.MatchString(c.BaselineCommit) || strings.TrimSpace(c.Goal) == "" || len(c.Goal) > MaxGoalLength {
		return fmt.Errorf("%w: contract identity, baseline, goal, and revision are required", ErrInvalidRecord)
	}
	if len(c.AllowedPaths) == 0 || len(c.AllowedPaths) > MaxScopeEntries || len(c.ForbiddenActions) > MaxScopeEntries || len(c.AcceptanceCriteria) == 0 || len(c.AcceptanceCriteria) > MaxAcceptanceItems {
		return fmt.Errorf("%w: contract collections are invalid", ErrInvalidRecord)
	}
	if c.Risk != RiskLow && c.Risk != RiskMedium && c.Risk != RiskHigh {
		return fmt.Errorf("%w: risk is invalid", ErrInvalidRecord)
	}
	if c.CreatedAt.IsZero() || c.EventID == "" {
		return fmt.Errorf("%w: contract provenance is required", ErrInvalidRecord)
	}
	if err := validateUnique(c.AllowedPaths, true); err != nil {
		return err
	}
	if err := validateUnique(c.ForbiddenActions, false); err != nil {
		return err
	}
	return validateUnique(c.AcceptanceCriteria, false)
}

func validateUnique(values []string, paths bool) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > MaxContractTextSize {
			return fmt.Errorf("%w: contract entry is invalid", ErrInvalidRecord)
		}
		if paths {
			clean := filepath.Clean(filepath.FromSlash(value))
			if filepath.IsAbs(clean) || filepath.VolumeName(clean) != "" || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
				return fmt.Errorf("%w: allowed path must remain repository-relative", ErrInvalidRecord)
			}
			value = filepath.ToSlash(clean)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("%w: duplicate contract entry", ErrInvalidRecord)
		}
		seen[value] = struct{}{}
	}
	return nil
}
