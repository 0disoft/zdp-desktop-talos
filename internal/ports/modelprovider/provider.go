package modelprovider

import (
	"context"
	"errors"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/planning"
)

var (
	ErrUnavailable     = errors.New("model provider is unavailable")
	ErrTimeout         = errors.New("model provider timed out")
	ErrRateLimited     = errors.New("model provider rate limited the request")
	ErrInvalidResponse = errors.New("model provider returned an invalid response")
)

type ContextBlock struct {
	ID          string
	Kind        string
	Authority   string
	SourceRef   string
	Sensitivity event.Sensitivity
	Content     string
}

type Request struct {
	RequestID       string
	ModelKey        string
	PromptVersion   string
	Instructions    string
	Context         []ContextBlock
	MaxOutputBytes  int
	MaxOutputTokens int
	MaxSteps        int
	MaxToolIntents  int
}

type Response struct {
	Plan           planning.Plan
	ProviderCallID string
	Usage          planning.Usage
}

type Provider interface {
	Key() string
	GeneratePlan(context.Context, Request) (Response, error)
}
