package memorycontext

import (
	"context"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
)

type Request struct {
	VaultID       string
	WorkspaceID   string
	Goal          string
	AllowedPaths  []string
	MaxCandidates int
	MaxItems      int
	MaxBytes      int
}

type Item struct {
	ID          string
	MemoryID    string
	Revision    int
	Kind        string
	SourceRef   string
	Sensitivity event.Sensitivity
	Content     string
	Reason      string
}

type Result struct {
	Items         []Item
	Considered    int
	SelectedBytes int
}

type Provider interface {
	Assemble(context.Context, Request) (Result, error)
}
