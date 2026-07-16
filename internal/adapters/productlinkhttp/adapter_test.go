package productlinkhttp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/ports/accountidentity"
)

func TestAdapterCompletesS256FlowWithoutLeakingVerifier(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	var createBody, exchangeBody []byte
	var exchangeCalls int
	var requestIDs []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		requestIDs = append(requestIDs, request.Header.Get("X-Request-ID"))
		if request.Header.Get("Idempotency-Key") != "link-command-1" || request.Header.Get("X-Trace-ID") != "correlation-1" || request.Header.Get("X-Request-ID") == "link-command-1" {
			t.Errorf("request metadata was not separated")
		}
		switch request.URL.Path {
		case "/create":
			createBody = readRequestBody(t, request)
			writer.WriteHeader(http.StatusCreated)
			fmt.Fprintf(writer, `{"challenge_ref":"challenge-1","verification_uri":%q,"expires_at":%q,"poll_interval_seconds":1}`, serverURL(request, "/verify?code=device-code"), now.Add(maximumLifetime).Format(time.RFC3339))
		case "/exchange":
			exchangeCalls++
			exchangeBody = readRequestBody(t, request)
			if exchangeCalls == 1 {
				writeErrorEnvelope(t, writer, request, http.StatusBadRequest, "authorization_pending", nil)
				return
			}
			if exchangeCalls == 2 {
				retryAfter := int64(12)
				writeErrorEnvelope(t, writer, request, http.StatusTooManyRequests, "slow_down", &retryAfter)
				return
			}
			if exchangeCalls == 3 {
				writer.Header().Set("Retry-After", "17")
				writeErrorEnvelope(t, writer, request, http.StatusTooManyRequests, "slow_down", nil)
				return
			}
			fmt.Fprintf(writer, `{"link_receipt_ref":"link-1","subject_ref":"subject-1","workspace_ref":"workspace-1","consent_receipt_ref":"consent-1","verified_at":%q}`, now.Add(10*time.Second).Format(time.RFC3339))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	clock := &fakeClock{now: now}
	browser := &recordingBrowser{}
	adapter, err := newAdapter(Config{Enabled: true, CreateURL: server.URL + "/create", ExchangeURL: server.URL + "/exchange", TrustedVerificationOrigin: server.URL}, server.Client(), browser, clock)
	if err != nil {
		t.Fatal(err)
	}
	adapter.requestIDs = &sequenceRequestIDs{values: []string{"req-create", "req-poll-1", "req-poll-2", "req-poll-3", "req-exchange"}}
	identity, err := adapter.Verify(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if identity.LinkReceiptRef != "link-1" || identity.SubjectRef != "subject-1" || identity.WorkspaceRef != "workspace-1" || identity.ConsentReceiptRef != "consent-1" {
		t.Fatalf("unexpected identity: %+v", identity)
	}
	if len(clock.waits) != 4 || clock.waits[0] != minimumPollInterval || clock.waits[1] != minimumPollInterval || clock.waits[2] != 12*time.Second || clock.waits[3] != 17*time.Second {
		t.Fatalf("poll waits=%v", clock.waits)
	}
	if strings.Join(requestIDs, ",") != "req-create,req-poll-1,req-poll-2,req-poll-3,req-exchange" {
		t.Fatalf("request IDs were not unique per call: %v", requestIDs)
	}

	var created createRequest
	if err := json.Unmarshal(createBody, &created); err != nil {
		t.Fatal(err)
	}
	var exchanged exchangeRequest
	if err := json.Unmarshal(exchangeBody, &exchanged); err != nil {
		t.Fatal(err)
	}
	verifierBytes, err := base64.RawURLEncoding.DecodeString(exchanged.ProofVerifier)
	if err != nil || len(verifierBytes) != 32 {
		t.Fatalf("verifier is not 32 random octets: length=%d err=%v", len(verifierBytes), err)
	}
	digest := sha256.Sum256([]byte(exchanged.ProofVerifier))
	if created.ProofMethod != "S256" || created.ProofChallenge != base64.RawURLEncoding.EncodeToString(digest[:]) {
		t.Fatalf("S256 challenge mismatch")
	}
	if bytesContain(createBody, exchanged.ProofVerifier) || bytesContain([]byte(browser.opened), exchanged.ProofVerifier) || strings.Contains(browser.opened, "proof_verifier") {
		t.Fatal("proof verifier escaped the exchange request")
	}
}

func TestAdapterMapsTerminalChallengeStates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status int
		code   string
		want   error
	}{{"denied", http.StatusBadRequest, "access_denied", accountidentity.ErrDenied}, {"expired", http.StatusBadRequest, "challenge_expired", accountidentity.ErrExpired}, {"consumed", http.StatusConflict, "challenge_already_consumed", accountidentity.ErrConsumed}}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			now := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
			server := newFlowServer(t, now, func(writer http.ResponseWriter, request *http.Request) {
				writeErrorEnvelope(t, writer, request, test.status, test.code, nil)
			})
			defer server.Close()
			adapter, err := newAdapter(testConfig(server.URL), server.Client(), &recordingBrowser{}, &fakeClock{now: now})
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.Verify(context.Background(), validRequest())
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
		})
	}
}

func TestAdapterEnforcesTenMinuteDeadlineAndCancellation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	server := newFlowServer(t, now, func(writer http.ResponseWriter, request *http.Request) {
		writeErrorEnvelope(t, writer, request, http.StatusBadRequest, "authorization_pending", nil)
	})
	defer server.Close()
	clock := &fakeClock{now: now}
	adapter, err := newAdapter(testConfig(server.URL), server.Client(), &recordingBrowser{}, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Verify(context.Background(), validRequest()); !errors.Is(err, accountidentity.ErrExpired) {
		t.Fatalf("deadline error=%v", err)
	}
	if elapsed := clock.now.Sub(now); elapsed != maximumLifetime {
		t.Fatalf("elapsed=%v", elapsed)
	}

	cancelClock := &fakeClock{now: now, waitErr: context.Canceled}
	adapter, err = newAdapter(testConfig(server.URL), server.Client(), &recordingBrowser{}, cancelClock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Verify(context.Background(), validRequest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}
}

func TestAdapterFailsClosedWhenDisabledOrVerificationURIIsUntrusted(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	browser := &recordingBrowser{}
	server := newFlowServer(t, now, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	})
	defer server.Close()
	disabled := testConfig(server.URL)
	disabled.Enabled = false
	adapter, err := newAdapter(disabled, server.Client(), browser, &fakeClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Verify(context.Background(), validRequest()); !errors.Is(err, accountidentity.ErrDisabled) {
		t.Fatalf("disabled error=%v", err)
	}

	untrusted := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		fmt.Fprintf(writer, `{"challenge_ref":"challenge-1","verification_uri":"https://example.invalid/verify","expires_at":%q,"poll_interval_seconds":5}`, now.Add(maximumLifetime).Format(time.RFC3339))
	}))
	defer untrusted.Close()
	adapter, err = newAdapter(Config{Enabled: true, CreateURL: untrusted.URL, ExchangeURL: untrusted.URL, TrustedVerificationOrigin: untrusted.URL}, untrusted.Client(), browser, &fakeClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Verify(context.Background(), validRequest()); !errors.Is(err, accountidentity.ErrRejected) || browser.opened != "" {
		t.Fatalf("untrusted URI error=%v opened=%q", err, browser.opened)
	}
}

func TestAdapterRejectsTokenOrSessionMaterialInsteadOfReturningIt(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	server := newFlowServer(t, now, func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(writer, `{"link_receipt_ref":"link-1","subject_ref":"subject-1","consent_receipt_ref":"consent-1","verified_at":%q,"access_token":"must-not-cross"}`, now.Add(minimumPollInterval).Format(time.RFC3339))
	})
	defer server.Close()
	adapter, err := newAdapter(testConfig(server.URL), server.Client(), &recordingBrowser{}, &fakeClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := adapter.Verify(context.Background(), validRequest())
	if !errors.Is(err, accountidentity.ErrRejected) || identity.SubjectRef != "" || identity.LinkReceiptRef != "" {
		t.Fatalf("unsafe response crossed the port: identity=%+v error=%v", identity, err)
	}
}

func TestAdapterDropsCallerCookieJarAndSetsNoCredentialHeaders(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	var sensitiveHeaders []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if value := request.Header.Get("Cookie"); value != "" {
			sensitiveHeaders = append(sensitiveHeaders, "Cookie="+value)
		}
		if value := request.Header.Get("Authorization"); value != "" {
			sensitiveHeaders = append(sensitiveHeaders, "Authorization="+value)
		}
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/create" {
			writer.WriteHeader(http.StatusCreated)
			fmt.Fprintf(writer, `{"challenge_ref":"challenge-1","verification_uri":%q,"expires_at":%q,"poll_interval_seconds":5}`, serverURL(request, "/verify"), now.Add(maximumLifetime).Format(time.RFC3339))
			return
		}
		fmt.Fprintf(writer, `{"link_receipt_ref":"link-1","subject_ref":"subject-1","consent_receipt_ref":"consent-1","verified_at":%q}`, now.Add(minimumPollInterval).Format(time.RFC3339))
	}))
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	serverURLValue, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(serverURLValue, []*http.Cookie{{Name: "browser_session", Value: "must-not-cross", Path: "/", Secure: true}})
	client := server.Client()
	client.Jar = jar
	adapter, err := newAdapter(testConfig(server.URL), client, &recordingBrowser{}, &fakeClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Verify(context.Background(), validRequest()); err != nil {
		t.Fatal(err)
	}
	if len(sensitiveHeaders) != 0 {
		t.Fatalf("credential headers crossed into product-link transport: %v", sensitiveHeaders)
	}
}

func TestAdapterRejectsNonContractSuccessAndMalformedErrorEnvelopes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)

	t.Run("create requires 201", func(t *testing.T) {
		browser := &recordingBrowser{}
		server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			fmt.Fprintf(writer, `{"challenge_ref":"challenge-1","verification_uri":%q,"expires_at":%q,"poll_interval_seconds":5}`, serverURL(request, "/verify"), now.Add(maximumLifetime).Format(time.RFC3339))
		}))
		defer server.Close()
		adapter, err := newAdapter(Config{Enabled: true, CreateURL: server.URL, ExchangeURL: server.URL, TrustedVerificationOrigin: server.URL}, server.Client(), browser, &fakeClock{now: now})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := adapter.Verify(context.Background(), validRequest()); !errors.Is(err, accountidentity.ErrRejected) || browser.opened != "" {
			t.Fatalf("non-201 create was accepted: error=%v opened=%q", err, browser.opened)
		}
	})

	for _, test := range []struct {
		name string
		body string
	}{
		{"unknown code", `{"code":"new_server_code","message":"safe","request_id":"%s","trace_id":"%s"}`},
		{"missing request id", `{"code":"authorization_pending","message":"safe","trace_id":"%[2]s"}`},
		{"invented state response", `{"state":"pending"}`},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			server := newFlowServer(t, now, func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(writer, test.body, request.Header.Get("X-Request-ID"), request.Header.Get("X-Trace-ID"))
			})
			defer server.Close()
			adapter, err := newAdapter(testConfig(server.URL), server.Client(), &recordingBrowser{}, &fakeClock{now: now})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.Verify(context.Background(), validRequest()); !errors.Is(err, accountidentity.ErrRejected) {
				t.Fatalf("malformed envelope error=%v", err)
			}
		})
	}
}

func TestAdapterRejectsUnboundedOrMalformedSlowDownHints(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name       string
		retryAfter string
		seconds    *int64
	}{
		{name: "body exceeds flow lifetime", seconds: int64Pointer(601)},
		{name: "header is malformed", retryAfter: "later"},
		{name: "header exceeds flow lifetime", retryAfter: "601"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			server := newFlowServer(t, now, func(writer http.ResponseWriter, request *http.Request) {
				if test.retryAfter != "" {
					writer.Header().Set("Retry-After", test.retryAfter)
				}
				writeErrorEnvelope(t, writer, request, http.StatusTooManyRequests, "slow_down", test.seconds)
			})
			defer server.Close()
			adapter, err := newAdapter(testConfig(server.URL), server.Client(), &recordingBrowser{}, &fakeClock{now: now})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.Verify(context.Background(), validRequest()); !errors.Is(err, accountidentity.ErrRejected) {
				t.Fatalf("invalid retry hint error=%v", err)
			}
		})
	}
}

func TestSensitiveMaterialHasNoPersistenceWailsUIOrProductionWiringSurface(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	paths := []string{filepath.Join(root, "main.go"), filepath.Join(root, "internal", "transport", "wailsapi"), filepath.Join(root, "internal", "adapters", "sqliteevent"), filepath.Join(root, "frontend", "src")}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		check := func(file string) {
			content, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			lower := strings.ToLower(string(content))
			for _, forbidden := range []string{"proof_verifier", "code_verifier", "access_token", "refresh_token", "session_token", "productlinkhttp"} {
				if strings.Contains(lower, forbidden) {
					t.Errorf("%s exposes forbidden product-link material or production wiring %q", file, forbidden)
				}
			}
		}
		if !info.IsDir() {
			check(path)
			continue
		}
		err = filepath.WalkDir(path, func(file string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && (strings.HasSuffix(file, ".go") || strings.HasSuffix(file, ".ts") || strings.HasSuffix(file, ".svelte")) {
				check(file)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

type fakeClock struct {
	now     time.Time
	waits   []time.Duration
	waitErr error
}

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) Wait(_ context.Context, duration time.Duration) error {
	c.waits = append(c.waits, duration)
	if c.waitErr != nil {
		return c.waitErr
	}
	c.now = c.now.Add(duration)
	return nil
}

type recordingBrowser struct{ opened string }

func (b *recordingBrowser) Open(ctx context.Context, uri string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.opened = uri
	return nil
}

func validRequest() accountidentity.Request {
	return accountidentity.Request{ProductRef: "talos", ClientInstanceRef: "instance-1", ClientCorrelationRef: "correlation-1", RequestedScopeRefs: []string{"account.link"}, IdempotencyKey: "link-command-1"}
}

func testConfig(serverURL string) Config {
	return Config{Enabled: true, CreateURL: serverURL + "/create", ExchangeURL: serverURL + "/exchange", TrustedVerificationOrigin: serverURL}
}

func newFlowServer(t *testing.T, now time.Time, exchange func(http.ResponseWriter, *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/create" {
			writer.WriteHeader(http.StatusCreated)
			fmt.Fprintf(writer, `{"challenge_ref":"challenge-1","verification_uri":%q,"expires_at":%q,"poll_interval_seconds":5}`, serverURL(request, "/verify"), now.Add(maximumLifetime).Format(time.RFC3339))
			return
		}
		exchange(writer, request)
	}))
}

func writeErrorEnvelope(t *testing.T, writer http.ResponseWriter, request *http.Request, status int, code string, retryAfter *int64) {
	t.Helper()
	envelope := map[string]any{"code": code, "message": "safe product-link error", "request_id": request.Header.Get("X-Request-ID"), "trace_id": request.Header.Get("X-Trace-ID")}
	if retryAfter != nil {
		envelope["retry_after_seconds"] = *retryAfter
	}
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(envelope); err != nil {
		t.Fatal(err)
	}
}

func serverURL(request *http.Request, path string) string {
	return "https://" + request.Host + path
}

func readRequestBody(t *testing.T, request *http.Request) []byte {
	t.Helper()
	defer request.Body.Close()
	data, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func bytesContain(data []byte, value string) bool { return strings.Contains(string(data), value) }

func int64Pointer(value int64) *int64 { return &value }

type sequenceRequestIDs struct {
	values []string
	index  int
}

func (g *sequenceRequestIDs) New() (string, error) {
	if g.index >= len(g.values) {
		return "", errors.New("request ID sequence exhausted")
	}
	value := g.values[g.index]
	g.index++
	return value, nil
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("repository root not found")
		}
		directory = parent
	}
}
