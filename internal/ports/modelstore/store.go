package modelstore

import (
	"context"
	"errors"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/planning"
)

var (
	ErrInvalidCommand      = errors.New("invalid model egress command")
	ErrNotFound            = errors.New("model egress receipt was not found")
	ErrConflict            = errors.New("model egress receipt state conflict")
	ErrIdempotencyConflict = errors.New("model egress idempotency conflict")
)

type PrepareInput struct {
	VaultID          string
	TaskID           string
	ContractRevision int
	ProviderKey      string
	ModelKey         string
	RequestID        string
	PromptVersion    string
	ContextHash      string
	RequestHash      string
	ContextItems     int
	InputBytes       int
	RedactionCount   int
	OccurredAt       time.Time
	IdempotencyKey   string
}

type FinishInput struct {
	VaultID        string
	ReceiptID      string
	ExpectedStatus planning.EgressStatus
	NextStatus     planning.EgressStatus
	ResponseHash   string
	ProviderCallID string
	OutputBytes    int
	Usage          planning.Usage
	SafeErrorCode  string
	OccurredAt     time.Time
	IdempotencyKey string
}

type Store interface {
	PrepareModelEgress(context.Context, PrepareInput) (planning.EgressReceipt, error)
	FinishModelEgress(context.Context, FinishInput) (planning.EgressReceipt, error)
	GetModelEgress(context.Context, string) (planning.EgressReceipt, error)
}
