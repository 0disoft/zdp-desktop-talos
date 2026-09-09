package taskstore

import "context"

type ListInput struct {
	VaultID        string
	WorkspaceRoot  string
	BaselineCommit string
	Limit          int
}

// Discovery is a read-only capability separate from task mutation.
type Discovery interface {
	ListTaskContracts(context.Context, ListInput) ([]Created, error)
}
