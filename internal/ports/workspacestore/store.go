package workspacestore

import (
	"context"
	"errors"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspacemapping"
)

var (
	ErrInvalidCommand   = errors.New("invalid workspace mapping command")
	ErrMappingRequired  = errors.New("workspace mapping is required on this device")
	ErrNotFound         = errors.New("workspace mapping was not found")
	ErrConflict         = errors.New("workspace mapping conflicts with current state")
	ErrRevisionConflict = errors.New("workspace mapping revision conflict")
)

type BindInput struct {
	WorkspaceID         string
	VaultID             string
	SourceWorkspaceHash string
	LocalRoot           string
	VerifiedBaseline    string
	ExpectedRevision    int
	OccurredAt          time.Time
	IdempotencyKey      string
}

type RevokeInput struct {
	WorkspaceID      string
	VaultID          string
	ExpectedRevision int
	OccurredAt       time.Time
	IdempotencyKey   string
}

type TaskWorkspace struct {
	TaskID      string
	Requirement workspacemapping.Requirement
}

type Store interface {
	GetTaskWorkspace(context.Context, string, string) (workspacemapping.Requirement, error)
	GetWorkspaceMapping(context.Context, string, string) (workspacemapping.Record, error)
	ListTaskWorkspaces(context.Context, string, int) ([]TaskWorkspace, error)
	BindWorkspaceMapping(context.Context, BindInput) (workspacemapping.Record, bool, error)
	RevokeWorkspaceMapping(context.Context, RevokeInput) (workspacemapping.Record, bool, error)
}
