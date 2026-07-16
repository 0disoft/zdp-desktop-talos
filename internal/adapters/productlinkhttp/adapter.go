package productlinkhttp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	accountdomain "github.com/0disoft/zdp-desktop-talos/internal/domain/accountlink"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/accountidentity"
)

const (
	minimumPollInterval = 5 * time.Second
	maximumLifetime     = 10 * time.Minute
	maximumResponseSize = 64 << 10
)

var errUnsafeResponse = errors.New("unsafe ZDP product-link response")

type Browser interface {
	Open(context.Context, string) error
}

type Config struct {
	Enabled                   bool
	CreateURL                 string
	ExchangeURL               string
	TrustedVerificationOrigin string
}

type Adapter struct {
	config     Config
	client     *http.Client
	browser    Browser
	clock      clock
	requestIDs requestIDGenerator
}

type requestIDGenerator interface {
	New() (string, error)
}

type cryptoRequestIDGenerator struct{}

func (cryptoRequestIDGenerator) New() (string, error) {
	value := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return "", err
	}
	defer clear(value)
	return "req_" + base64.RawURLEncoding.EncodeToString(value), nil
}

type clock interface {
	Now() time.Time
	Wait(context.Context, time.Duration) error
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func New(config Config, client *http.Client, browser Browser) (*Adapter, error) {
	return newAdapter(config, client, browser, realClock{})
}

func newAdapter(config Config, client *http.Client, browser Browser, adapterClock clock) (*Adapter, error) {
	if client == nil || browser == nil || adapterClock == nil {
		return nil, accountidentity.ErrInvalidRequest
	}
	if _, err := validateEndpoint(config.CreateURL); err != nil {
		return nil, err
	}
	if _, err := validateEndpoint(config.ExchangeURL); err != nil {
		return nil, err
	}
	if _, err := validateOrigin(config.TrustedVerificationOrigin); err != nil {
		return nil, err
	}
	boundedClient := *client
	boundedClient.Jar = nil
	boundedClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Adapter{config: config, client: &boundedClient, browser: browser, clock: adapterClock, requestIDs: cryptoRequestIDGenerator{}}, nil
}

func (a *Adapter) Verify(ctx context.Context, request accountidentity.Request) (accountdomain.VerifiedIdentity, error) {
	if a == nil || !a.config.Enabled {
		return accountdomain.VerifiedIdentity{}, accountidentity.ErrDisabled
	}
	if a.requestIDs == nil {
		return accountdomain.VerifiedIdentity{}, accountidentity.ErrUnavailable
	}
	if ctx == nil || !validRef(request.ProductRef) || !validRef(request.ClientInstanceRef) || !validRef(request.ClientCorrelationRef) || !validRef(request.IdempotencyKey) || !validScopes(request.RequestedScopeRefs) {
		return accountdomain.VerifiedIdentity{}, accountidentity.ErrInvalidRequest
	}
	verifierBytes := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, verifierBytes); err != nil {
		return accountdomain.VerifiedIdentity{}, accountidentity.ErrUnavailable
	}
	defer clear(verifierBytes)
	proofVerifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	challengeDigest := sha256.Sum256([]byte(proofVerifier))
	proofChallenge := base64.RawURLEncoding.EncodeToString(challengeDigest[:])

	created, err := a.create(ctx, request, proofChallenge)
	if err != nil {
		return accountdomain.VerifiedIdentity{}, err
	}
	verificationURI, err := a.validateVerificationURI(created.VerificationURI, proofVerifier)
	if err != nil {
		return accountdomain.VerifiedIdentity{}, err
	}
	if err := a.browser.Open(ctx, verificationURI.String()); err != nil {
		return accountdomain.VerifiedIdentity{}, accountidentity.ErrUnavailable
	}

	deadline := a.clock.Now().Add(maximumLifetime)
	if created.ExpiresAt.Before(deadline) {
		deadline = created.ExpiresAt
	}
	interval := time.Duration(created.PollIntervalSeconds) * time.Second
	if interval < minimumPollInterval {
		interval = minimumPollInterval
	}
	for {
		remaining := deadline.Sub(a.clock.Now())
		if remaining <= 0 || remaining < interval {
			return accountdomain.VerifiedIdentity{}, accountidentity.ErrExpired
		}
		if err := a.clock.Wait(ctx, interval); err != nil {
			return accountdomain.VerifiedIdentity{}, err
		}
		identity, pending, retryDelay, err := a.exchange(ctx, request, created.ChallengeRef, proofVerifier)
		if err != nil {
			return accountdomain.VerifiedIdentity{}, err
		}
		if !pending {
			return identity, nil
		}
		if retryDelay > interval {
			interval = retryDelay
		}
	}
}

type createRequest struct {
	ProductRef           string   `json:"product_ref"`
	ClientInstanceRef    string   `json:"client_instance_ref"`
	ClientCorrelationRef string   `json:"client_correlation_ref"`
	ProofChallenge       string   `json:"proof_challenge"`
	ProofMethod          string   `json:"proof_method"`
	RequestedScopeRefs   []string `json:"requested_scope_refs"`
}

type createResponse struct {
	ChallengeRef        string    `json:"challenge_ref"`
	VerificationURI     string    `json:"verification_uri"`
	ExpiresAtValue      string    `json:"expires_at"`
	PollIntervalSeconds int       `json:"poll_interval_seconds"`
	ExpiresAt           time.Time `json:"-"`
}

func (a *Adapter) create(ctx context.Context, request accountidentity.Request, challenge string) (createResponse, error) {
	payload := createRequest{ProductRef: request.ProductRef, ClientInstanceRef: request.ClientInstanceRef, ClientCorrelationRef: request.ClientCorrelationRef, ProofChallenge: challenge, ProofMethod: "S256", RequestedScopeRefs: append([]string(nil), request.RequestedScopeRefs...)}
	var response createResponse
	httpResponse, err := a.post(ctx, a.config.CreateURL, request, payload)
	if err != nil {
		if errors.Is(err, errUnsafeResponse) {
			return createResponse{}, accountidentity.ErrRejected
		}
		return createResponse{}, accountidentity.ErrUnavailable
	}
	if httpResponse.status != http.StatusCreated {
		envelope, envelopeErr := decodeErrorEnvelope(httpResponse, request.ClientCorrelationRef)
		if envelopeErr != nil {
			return createResponse{}, accountidentity.ErrRejected
		}
		switch envelope.Code {
		case "rate_limited":
			return createResponse{}, accountidentity.ErrUnavailable
		case "validation_failed", "product_not_allowed", "scope_not_allowed", "idempotency_conflict":
			return createResponse{}, accountidentity.ErrRejected
		default:
			return createResponse{}, accountidentity.ErrRejected
		}
	}
	if err := json.Unmarshal(httpResponse.body, &response); err != nil {
		return createResponse{}, accountidentity.ErrRejected
	}
	if !validRef(response.ChallengeRef) || response.PollIntervalSeconds < 0 {
		return createResponse{}, accountidentity.ErrRejected
	}
	expiresAt, err := time.Parse(time.RFC3339, response.ExpiresAtValue)
	if err != nil || !expiresAt.After(a.clock.Now()) {
		return createResponse{}, accountidentity.ErrRejected
	}
	response.ExpiresAt = expiresAt
	return response, nil
}

type exchangeRequest struct {
	ChallengeRef         string `json:"challenge_ref"`
	ClientCorrelationRef string `json:"client_correlation_ref"`
	ProofVerifier        string `json:"proof_verifier"`
}

type exchangeResponse struct {
	LinkReceiptRef    string `json:"link_receipt_ref"`
	SubjectRef        string `json:"subject_ref"`
	WorkspaceRef      string `json:"workspace_ref"`
	ConsentReceiptRef string `json:"consent_receipt_ref"`
	VerifiedAtValue   string `json:"verified_at"`
}

func (a *Adapter) exchange(ctx context.Context, request accountidentity.Request, challengeRef, verifier string) (accountdomain.VerifiedIdentity, bool, time.Duration, error) {
	payload := exchangeRequest{ChallengeRef: challengeRef, ClientCorrelationRef: request.ClientCorrelationRef, ProofVerifier: verifier}
	var response exchangeResponse
	httpResponse, err := a.post(ctx, a.config.ExchangeURL, request, payload)
	if err != nil {
		if errors.Is(err, errUnsafeResponse) {
			return accountdomain.VerifiedIdentity{}, false, 0, accountidentity.ErrRejected
		}
		return accountdomain.VerifiedIdentity{}, false, 0, accountidentity.ErrUnavailable
	}
	if httpResponse.status != http.StatusOK {
		envelope, envelopeErr := decodeErrorEnvelope(httpResponse, request.ClientCorrelationRef)
		if envelopeErr != nil {
			return accountdomain.VerifiedIdentity{}, false, 0, accountidentity.ErrRejected
		}
		retryDelay, retryErr := a.retryDelay(httpResponse, envelope)
		if retryErr != nil {
			return accountdomain.VerifiedIdentity{}, false, 0, accountidentity.ErrRejected
		}
		switch envelope.Code {
		case "authorization_pending":
			return accountdomain.VerifiedIdentity{}, true, retryDelay, nil
		case "slow_down":
			if retryDelay < minimumPollInterval*2 {
				retryDelay = minimumPollInterval * 2
			}
			return accountdomain.VerifiedIdentity{}, true, retryDelay, nil
		case "access_denied":
			return accountdomain.VerifiedIdentity{}, false, 0, accountidentity.ErrDenied
		case "challenge_expired":
			return accountdomain.VerifiedIdentity{}, false, 0, accountidentity.ErrExpired
		case "challenge_already_consumed":
			return accountdomain.VerifiedIdentity{}, false, 0, accountidentity.ErrConsumed
		default:
			return accountdomain.VerifiedIdentity{}, false, 0, accountidentity.ErrRejected
		}
	}
	if err := json.Unmarshal(httpResponse.body, &response); err != nil {
		return accountdomain.VerifiedIdentity{}, false, 0, accountidentity.ErrRejected
	}
	verifiedAt, err := time.Parse(time.RFC3339, response.VerifiedAtValue)
	if err != nil || !validRef(response.LinkReceiptRef) {
		return accountdomain.VerifiedIdentity{}, false, 0, accountidentity.ErrRejected
	}
	identity := accountdomain.VerifiedIdentity{LinkReceiptRef: response.LinkReceiptRef, SubjectRef: response.SubjectRef, WorkspaceRef: response.WorkspaceRef, ConsentReceiptRef: response.ConsentReceiptRef, VerifiedAt: verifiedAt}
	if err := identity.Validate(); err != nil {
		return accountdomain.VerifiedIdentity{}, false, 0, accountidentity.ErrRejected
	}
	return identity, false, 0, nil
}

type httpResponse struct {
	status    int
	header    http.Header
	body      []byte
	requestID string
}

func (a *Adapter) post(ctx context.Context, endpoint string, request accountidentity.Request, payload any) (httpResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return httpResponse{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return httpResponse{}, err
	}
	requestID, err := a.requestIDs.New()
	if err != nil || !validRef(requestID) || requestID == request.IdempotencyKey || requestID == request.ClientCorrelationRef {
		return httpResponse{}, accountidentity.ErrUnavailable
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("X-Request-ID", requestID)
	httpRequest.Header.Set("X-Trace-ID", request.ClientCorrelationRef)
	httpRequest.Header.Set("Idempotency-Key", request.IdempotencyKey)
	httpRequest.Header.Del("Authorization")
	httpRequest.Header.Del("Cookie")
	response, err := a.client.Do(httpRequest)
	if err != nil {
		return httpResponse{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseSize+1))
	if err != nil || len(data) > maximumResponseSize {
		return httpResponse{}, accountidentity.ErrRejected
	}
	if containsForbiddenField(data) {
		return httpResponse{}, errUnsafeResponse
	}
	return httpResponse{status: response.StatusCode, header: response.Header.Clone(), body: data, requestID: requestID}, nil
}

type errorEnvelope struct {
	Code              string `json:"code"`
	Message           string `json:"message"`
	RequestID         string `json:"request_id"`
	TraceID           string `json:"trace_id"`
	RetryAfterSeconds *int64 `json:"retry_after_seconds,omitempty"`
}

func decodeErrorEnvelope(response httpResponse, expectedTraceID string) (errorEnvelope, error) {
	if response.status < 400 || response.status > 599 || len(bytes.TrimSpace(response.body)) == 0 {
		return errorEnvelope{}, accountidentity.ErrRejected
	}
	var envelope errorEnvelope
	if err := json.Unmarshal(response.body, &envelope); err != nil || !validRef(envelope.Code) || strings.TrimSpace(envelope.Message) == "" || len(envelope.Message) > 1024 || envelope.RequestID != response.requestID || envelope.TraceID != expectedTraceID {
		return errorEnvelope{}, accountidentity.ErrRejected
	}
	return envelope, nil
}

func (a *Adapter) retryDelay(response httpResponse, envelope errorEnvelope) (time.Duration, error) {
	var delay time.Duration
	if envelope.RetryAfterSeconds != nil {
		if *envelope.RetryAfterSeconds < 1 || *envelope.RetryAfterSeconds > int64(maximumLifetime/time.Second) {
			return 0, accountidentity.ErrRejected
		}
		delay = time.Duration(*envelope.RetryAfterSeconds) * time.Second
	}
	if value := strings.TrimSpace(response.header.Get("Retry-After")); value != "" {
		headerDelay, err := parseRetryAfter(value, a.clock.Now())
		if err != nil {
			return 0, accountidentity.ErrRejected
		}
		if headerDelay > delay {
			delay = headerDelay
		}
	}
	return delay, nil
}

func parseRetryAfter(value string, now time.Time) (time.Duration, error) {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds < 1 || seconds > int64(maximumLifetime/time.Second) {
			return 0, accountidentity.ErrRejected
		}
		return time.Duration(seconds) * time.Second, nil
	}
	timestamp, err := http.ParseTime(value)
	if err != nil {
		return 0, accountidentity.ErrRejected
	}
	delay := timestamp.Sub(now)
	if delay < time.Second || delay > maximumLifetime {
		return 0, accountidentity.ErrRejected
	}
	return delay, nil
}

func (a *Adapter) validateVerificationURI(raw, verifier string) (*url.URL, error) {
	verificationURI, err := url.Parse(raw)
	if err != nil || verificationURI.Scheme != "https" || verificationURI.Host == "" || verificationURI.User != nil || verificationURI.Fragment != "" || strings.Contains(raw, verifier) {
		return nil, accountidentity.ErrRejected
	}
	trusted, _ := validateOrigin(a.config.TrustedVerificationOrigin)
	if verificationURI.Scheme != trusted.Scheme || !strings.EqualFold(verificationURI.Host, trusted.Host) {
		return nil, accountidentity.ErrRejected
	}
	for key := range verificationURI.Query() {
		if forbiddenField(key) {
			return nil, accountidentity.ErrRejected
		}
	}
	return verificationURI, nil
}

func validateEndpoint(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, fmt.Errorf("%w: invalid HTTPS endpoint", accountidentity.ErrInvalidRequest)
	}
	return parsed, nil
}

func validateOrigin(raw string) (*url.URL, error) {
	parsed, err := validateEndpoint(raw)
	if err != nil || parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" {
		return nil, fmt.Errorf("%w: invalid trusted verification origin", accountidentity.ErrInvalidRequest)
	}
	return parsed, nil
}

func containsForbiddenField(data []byte) bool {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return false
	}
	var visit func(any) bool
	visit = func(current any) bool {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				if forbiddenField(key) || visit(child) {
					return true
				}
			}
		case []any:
			for _, child := range typed {
				if visit(child) {
					return true
				}
			}
		}
		return false
	}
	return visit(value)
}

func forbiddenField(value string) bool {
	switch strings.ToLower(value) {
	case "proof_verifier", "password", "authorization", "authorization_header", "cookie", "cookie_header", "access_token", "refresh_token", "refresh_token_plaintext", "provider_secret", "session", "session_token", "raw_customer_payload", "raw_provider_error", "stack_trace", "screen_component_payload":
		return true
	default:
		return false
	}
}

func validScopes(values []string) bool {
	if len(values) == 0 || len(values) > 16 {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validRef(value) {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validRef(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.' || char == ':' {
			continue
		}
		return false
	}
	return true
}
