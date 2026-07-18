package openairesponses

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/planning"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/modelprovider"
)

const (
	ProviderKey       = "openai-responses"
	endpoint          = "https://api.openai.com/v1/responses"
	credentialEnvName = "OPENAI_API_KEY"
	maxWireBytes      = 2 << 20
)

type CredentialSource interface {
	Load(context.Context) ([]byte, error)
}

type EnvironmentCredential struct{}

func (EnvironmentCredential) Load(context.Context) ([]byte, error) {
	value, ok := os.LookupEnv(credentialEnvName)
	if !ok || strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\r\n") {
		return nil, modelprovider.ErrUnavailable
	}
	return []byte(value), nil
}

type Provider struct {
	credentials CredentialSource
	client      *http.Client
}

func New(credentials CredentialSource, client *http.Client) (*Provider, error) {
	if credentials == nil {
		return nil, modelprovider.ErrUnavailable
	}
	if client == nil {
		client = &http.Client{}
	}
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Provider{credentials: credentials, client: &bounded}, nil
}

func (p *Provider) Key() string { return ProviderKey }

func (p *Provider) GeneratePlan(ctx context.Context, request modelprovider.Request) (modelprovider.Response, error) {
	if p == nil || p.credentials == nil || p.client == nil || ctx == nil || !planning.ValidOpaqueID(request.RequestID) || !planning.ValidKey(request.ModelKey) || request.PromptVersion == "" || strings.TrimSpace(request.Instructions) == "" || len(request.Context) == 0 || request.MaxOutputBytes < 1 || request.MaxOutputBytes > 1<<20 || request.MaxSteps < 1 || request.MaxSteps > planning.MaxPlanSteps || request.MaxToolIntents < 1 || request.MaxToolIntents > planning.MaxPlanSteps {
		return modelprovider.Response{}, modelprovider.ErrInvalidResponse
	}
	key, err := p.credentials.Load(ctx)
	if err != nil {
		return modelprovider.Response{}, errors.Join(modelprovider.ErrUnavailable, err)
	}
	defer clear(key)
	if len(key) == 0 || bytes.ContainsAny(key, "\r\n") {
		return modelprovider.Response{}, modelprovider.ErrUnavailable
	}

	contextPayload, err := json.Marshal(struct {
		SchemaVersion int                          `json:"schema_version"`
		Blocks        []modelprovider.ContextBlock `json:"context_blocks"`
	}{SchemaVersion: 1, Blocks: request.Context})
	if err != nil {
		return modelprovider.Response{}, modelprovider.ErrInvalidResponse
	}
	wire := responseRequest{
		Model:        request.ModelKey,
		Instructions: request.Instructions,
		Input:        []inputMessage{{Role: "user", Content: []inputContent{{Type: "input_text", Text: string(contextPayload)}}}},
		Text: responseText{Format: responseFormat{
			Type: "json_schema", Name: "talos_plan", Description: "A bounded Talos verification plan", Strict: true,
			Schema: planSchema(request.MaxSteps, request.MaxToolIntents),
		}},
		MaxOutputTokens: maxOutputTokens(request.MaxOutputBytes),
		Store:           false,
	}
	body, err := json.Marshal(wire)
	if err != nil || len(body) > maxWireBytes {
		return modelprovider.Response{}, modelprovider.ErrInvalidResponse
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return modelprovider.Response{}, errors.Join(modelprovider.ErrUnavailable, err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+string(key))
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")

	httpResponse, err := p.client.Do(httpRequest)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return modelprovider.Response{}, errors.Join(modelprovider.ErrTimeout, ctx.Err())
		}
		return modelprovider.Response{}, errors.Join(modelprovider.ErrUnavailable, err)
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(httpResponse.Body, 4096))
		return modelprovider.Response{}, statusError(httpResponse.StatusCode)
	}

	limit := int64(request.MaxOutputBytes*4 + 64<<10)
	if limit > maxWireBytes {
		limit = maxWireBytes
	}
	encoded, err := io.ReadAll(io.LimitReader(httpResponse.Body, limit+1))
	if err != nil {
		return modelprovider.Response{}, errors.Join(modelprovider.ErrUnavailable, err)
	}
	if int64(len(encoded)) > limit {
		return modelprovider.Response{}, modelprovider.ErrInvalidResponse
	}
	var decoded responseEnvelope
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.Status != "completed" || !planning.ValidOpaqueID(decoded.ID) {
		return modelprovider.Response{}, modelprovider.ErrInvalidResponse
	}
	output, err := outputText(decoded)
	if err != nil || len(output) > request.MaxOutputBytes {
		return modelprovider.Response{}, modelprovider.ErrInvalidResponse
	}
	var plan planning.Plan
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil || decoder.Decode(&struct{}{}) != io.EOF || plan.Validate() != nil {
		return modelprovider.Response{}, modelprovider.ErrInvalidResponse
	}
	usage := planning.Usage{InputTokens: decoded.Usage.InputTokens, CachedInputTokens: decoded.Usage.InputTokenDetails.CachedTokens, OutputTokens: decoded.Usage.OutputTokens}
	if usage.Validate() != nil {
		return modelprovider.Response{}, modelprovider.ErrInvalidResponse
	}
	return modelprovider.Response{Plan: plan, ProviderCallID: decoded.ID, Usage: usage}, nil
}

func statusError(code int) error {
	switch code {
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return modelprovider.ErrTimeout
	case http.StatusTooManyRequests:
		return modelprovider.ErrRateLimited
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return modelprovider.ErrInvalidResponse
	default:
		return modelprovider.ErrUnavailable
	}
}

func outputText(response responseEnvelope) (string, error) {
	var result string
	for _, item := range response.Output {
		if item.Type != "message" {
			continue
		}
		for _, content := range item.Content {
			if content.Type == "refusal" || content.Refusal != "" {
				return "", modelprovider.ErrInvalidResponse
			}
			if content.Type != "output_text" {
				continue
			}
			if result != "" || strings.TrimSpace(content.Text) == "" {
				return "", modelprovider.ErrInvalidResponse
			}
			result = content.Text
		}
	}
	if result == "" {
		return "", modelprovider.ErrInvalidResponse
	}
	return result, nil
}

type responseRequest struct {
	Model           string         `json:"model"`
	Instructions    string         `json:"instructions"`
	Input           []inputMessage `json:"input"`
	Text            responseText   `json:"text"`
	MaxOutputTokens int            `json:"max_output_tokens"`
	Store           bool           `json:"store"`
}

type inputMessage struct {
	Role    string         `json:"role"`
	Content []inputContent `json:"content"`
}

type inputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responseText struct {
	Format responseFormat `json:"format"`
}

type responseFormat struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Strict      bool           `json:"strict"`
	Schema      map[string]any `json:"schema"`
}

type responseEnvelope struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Refusal string `json:"refusal"`
		} `json:"content"`
	} `json:"output"`
	Usage struct {
		InputTokens       int `json:"input_tokens"`
		OutputTokens      int `json:"output_tokens"`
		InputTokenDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"input_tokens_details"`
	} `json:"usage"`
}

func planSchema(maxSteps, maxTools int) map[string]any {
	maxItems := maxSteps
	if maxTools < maxItems {
		maxItems = maxTools
	}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"schema_version", "summary", "steps"},
		"properties": map[string]any{
			"schema_version": map[string]any{"type": "integer", "const": planning.SchemaVersion},
			"summary":        map[string]any{"type": "string", "minLength": 1, "maxLength": planning.MaxSummaryLength},
			"steps": map[string]any{
				"type": "array", "minItems": 1, "maxItems": maxItems,
				"items": map[string]any{
					"type": "object", "additionalProperties": false,
					"required": []string{"id", "purpose", "tool"},
					"properties": map[string]any{
						"id":      map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9._-]{0,63}$"},
						"purpose": map[string]any{"type": "string", "minLength": 1, "maxLength": planning.MaxStepPurpose},
						"tool": map[string]any{
							"type": "object", "additionalProperties": false,
							"required": []string{"id", "kind", "command_index"},
							"properties": map[string]any{
								"id":            map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9._-]{0,63}$"},
								"kind":          map[string]any{"type": "string", "const": string(planning.ToolVerificationCommand)},
								"command_index": map[string]any{"type": "integer", "minimum": 0},
							},
						},
					},
				},
			},
		},
	}
}

var _ modelprovider.Provider = (*Provider)(nil)
var _ CredentialSource = EnvironmentCredential{}

func maxOutputTokens(maxOutputBytes int) int {
	tokens := maxOutputBytes / 4
	if tokens < 256 {
		return 256
	}
	return tokens
}
