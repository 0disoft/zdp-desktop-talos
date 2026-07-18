package openairesponses

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/modelprovider"
)

func TestProviderCreatesBoundedStructuredResponseRequest(t *testing.T) {
	t.Parallel()
	const secret = "test-api-key-never-in-body"
	var calls int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.String() != endpoint || request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer "+secret {
			t.Fatalf("request=%s %s authorization=%q", request.Method, request.URL, request.Header.Get("Authorization"))
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), secret) {
			t.Fatal("credential leaked into request body")
		}
		var wire map[string]any
		if err := json.Unmarshal(body, &wire); err != nil {
			t.Fatal(err)
		}
		text, ok := wire["text"].(map[string]any)
		format, formatOK := text["format"].(map[string]any)
		if !ok || !formatOK || format["type"] != "json_schema" || format["strict"] != true || wire["store"] != false || wire["max_output_tokens"] != float64(4096) {
			t.Fatalf("wire=%+v", wire)
		}
		return jsonResponse(http.StatusOK, `{"id":"resp_1","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{\"schema_version\":1,\"summary\":\"Run focused verification.\",\"steps\":[{\"id\":\"verify\",\"purpose\":\"Run tests\",\"tool\":{\"id\":\"verify-tool\",\"kind\":\"verification_command\",\"command_index\":0}}]}"}]}],"usage":{"input_tokens":40,"output_tokens":20,"input_tokens_details":{"cached_tokens":5}}}`), nil
	})}
	provider, err := New(staticCredential(secret), client)
	if err != nil {
		t.Fatal(err)
	}
	response, err := provider.GeneratePlan(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || response.ProviderCallID != "resp_1" || response.Plan.Summary != "Run focused verification." || response.Usage.InputTokens != 40 || response.Usage.CachedInputTokens != 5 || response.Usage.OutputTokens != 20 {
		t.Fatalf("calls=%d response=%+v", calls, response)
	}
}

func TestProviderNeverFollowsRedirectWithCredential(t *testing.T) {
	t.Parallel()
	var calls int
	provider, err := New(staticCredential("test-api-key"), &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://attacker.invalid/collect"}}, Body: io.NopCloser(strings.NewReader("redirect"))}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.GeneratePlan(context.Background(), validRequest())
	if err != modelprovider.ErrUnavailable || calls != 1 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
}

func TestProviderMapsRateLimitAndRejectsMalformedOutput(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		code int
		body string
		want error
	}{
		{name: "rate limit", code: http.StatusTooManyRequests, body: `{"error":{"message":"ignored"}}`, want: modelprovider.ErrRateLimited},
		{name: "unknown plan field", code: http.StatusOK, body: `{"id":"resp_2","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{\"schema_version\":1,\"summary\":\"bad\",\"steps\":[],\"unexpected\":true}"}]}],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`, want: modelprovider.ErrInvalidResponse},
		{name: "refusal", code: http.StatusOK, body: `{"id":"resp_3","status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"cannot comply"}]}],"usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":0}}}`, want: modelprovider.ErrInvalidResponse},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			provider, err := New(staticCredential("test-api-key"), &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(test.code, test.body), nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.GeneratePlan(context.Background(), validRequest())
			if err != test.want {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
		})
	}
}

func validRequest() modelprovider.Request {
	return modelprovider.Request{
		RequestID: "request-1", ModelKey: "model-test", PromptVersion: "planning.v2", Instructions: "Return a plan.",
		Context:        []modelprovider.ContextBlock{{ID: "task-contract", Kind: "task_contract", Authority: "untrusted_data", SourceRef: "task-contract", Sensitivity: event.SensitivityPrivate, Content: "bounded context"}},
		MaxOutputBytes: 16 << 10, MaxSteps: 4, MaxToolIntents: 4,
	}
}

type staticCredential string

func (s staticCredential) Load(context.Context) ([]byte, error) { return []byte(s), nil }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
