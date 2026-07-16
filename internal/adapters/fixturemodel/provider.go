package fixturemodel

import (
	"context"
	"errors"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/planning"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/modelprovider"
)

type Provider struct {
	providerKey string
	modelKey    string
	commands    []int
}

func New(providerKey, modelKey string, commandIndexes []int) (*Provider, error) {
	if !planning.ValidKey(providerKey) || !planning.ValidKey(modelKey) || len(commandIndexes) == 0 || len(commandIndexes) > planning.MaxPlanSteps {
		return nil, modelprovider.ErrInvalidResponse
	}
	seen := make(map[int]struct{}, len(commandIndexes))
	for _, commandIndex := range commandIndexes {
		if commandIndex < 0 {
			return nil, modelprovider.ErrInvalidResponse
		}
		if _, exists := seen[commandIndex]; exists {
			return nil, modelprovider.ErrInvalidResponse
		}
		seen[commandIndex] = struct{}{}
	}
	return &Provider{providerKey: providerKey, modelKey: modelKey, commands: append([]int(nil), commandIndexes...)}, nil
}

func (p *Provider) Key() string {
	if p == nil {
		return ""
	}
	return p.providerKey
}

func (p *Provider) GeneratePlan(ctx context.Context, request modelprovider.Request) (modelprovider.Response, error) {
	if err := ctx.Err(); err != nil {
		return modelprovider.Response{}, err
	}
	if p == nil || request.ModelKey != p.modelKey || request.RequestID == "" || request.PromptVersion == "" || request.Instructions == "" || len(request.Context) == 0 || request.MaxSteps < len(p.commands) || request.MaxToolIntents < len(p.commands) || request.MaxOutputBytes <= 0 {
		return modelprovider.Response{}, modelprovider.ErrInvalidResponse
	}
	for _, block := range request.Context {
		if block.Authority != "untrusted_data" || block.Content == "" {
			return modelprovider.Response{}, modelprovider.ErrInvalidResponse
		}
	}
	steps := make([]planning.Step, len(p.commands))
	for index, commandIndex := range p.commands {
		steps[index] = planning.Step{
			ID: "step-" + digit(index), Purpose: "Run a Task Contract verification command.",
			Tool: planning.ToolIntent{ID: "tool-" + digit(index), Kind: planning.ToolVerificationCommand, CommandIndex: commandIndex},
		}
	}
	plan := planning.Plan{SchemaVersion: planning.SchemaVersion, Summary: "Run bounded contract verification.", Steps: steps}
	if err := plan.Validate(); err != nil {
		return modelprovider.Response{}, errors.Join(modelprovider.ErrInvalidResponse, err)
	}
	return modelprovider.Response{Plan: plan, ProviderCallID: "fixture-call", Usage: planning.Usage{}}, nil
}

func digit(value int) string {
	const digits = "0123456789"
	if value >= 0 && value < len(digits) {
		return string(digits[value])
	}
	return "x"
}
