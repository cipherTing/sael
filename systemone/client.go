package systemone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// Client calls the System One endpoint. It is safe for concurrent use: it holds
// no per-request state, and the underlying http.Client is safe by contract.
//
// Create one with New and reuse it. Creating a Client per request throws away
// connection pooling.
type Client struct {
	baseURL      string
	model        string
	timeout      time.Duration
	totalTimeout time.Duration
	retry        RetryPolicy
	header       http.Header
	log          *slog.Logger
	transport    *transport
}

// New builds a Client from its arguments and the environment.
//
// It performs no file I/O. Settings come from three layers, each overriding the
// one before it: built-in defaults, then environment variables
// (TYPESAFE_BASE_URL, TYPESAFE_DEFAULT_MODEL, TYPESAFE_API_KEY), then explicit
// Option values. Reading a configuration directory is a separate, deliberate
// step; see NewFromConfigDir and LoadConfig.
//
// It returns an error wrapping ErrValidation when no API key can be found or the
// base URL is not absolute, so misconfiguration fails at construction rather
// than on the first request.
func New(opts ...Option) (*Client, error) {
	return newClient(nil, opts)
}

// newClient applies the layers in order: file settings, then the environment,
// then the caller's Options. Splitting the file layer out is what keeps New pure
// while still letting a file act as a baseline rather than an override.
func newClient(fileOpts, opts []Option) (*Client, error) {
	cfg := defaultConfig()
	applyOptions(cfg, fileOpts)
	applyEnv(cfg)
	applyOptions(cfg, opts)

	apiKey := cfg.apiKey
	if apiKey == "" {
		return nil, fmt.Errorf("%w: no API key: set %s, pass WithAPIKey, or use NewFromConfigDir to read %s",
			ErrValidation, EnvAPIKey, filepath.Join(DefaultConfigDir(), AuthFileName))
	}

	baseURL := cfg.baseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if !strings.Contains(baseURL, "://") {
		return nil, fmt.Errorf("%w: base URL %q must be absolute, for example %q", ErrValidation, baseURL, DefaultBaseURL)
	}

	model := cfg.model
	if model == "" {
		model = DefaultModel
	}

	if cfg.timeout < 0 {
		return nil, fmt.Errorf("%w: timeout must not be negative", ErrValidation)
	}
	if cfg.totalTimeout < 0 {
		return nil, fmt.Errorf("%w: total timeout must not be negative", ErrValidation)
	}
	if err := validateRetryPolicy(cfg.retry); err != nil {
		return nil, err
	}

	logger := cfg.logger
	if logger == nil {
		// discardLogger, defined alongside the transport, exists because
		// slog.DiscardHandler landed in go1.24 and this module targets go1.23.
		logger = discardLogger()
	}

	hc := cfg.httpClient
	if hc == nil {
		// No http.Client.Timeout: deadlines are applied per attempt through the
		// context so that retries still get their own budget.
		//
		// Redirects are refused. This endpoint is a single POST to a fixed URL,
		// so a 3xx is not part of the contract, and the default Go policy would
		// silently rewrite the request: 301/302/303 turn the POST into a GET,
		// drop the body, and replay the Authorization header at the target the
		// server chose. Returning the response unchanged instead surfaces the
		// 3xx as an APIError the caller can act on.
		hc = &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}

	header := make(http.Header)
	for k, vs := range cfg.header {
		for _, v := range vs {
			header.Add(k, v)
		}
	}
	header.Set("Authorization", "Bearer "+apiKey)
	header.Set("Accept", "application/json")
	header.Set("Content-Type", "application/json")
	header.Set("User-Agent", UserAgent)

	tr := newTransport(baseURL, hc, cfg.timeout, cfg.retry, logger)
	tr.maxResponseBytes = cfg.maxResponseBytes

	return &Client{
		baseURL:      strings.TrimRight(baseURL, "/"),
		model:        model,
		timeout:      cfg.timeout,
		totalTimeout: cfg.totalTimeout,
		retry:        cfg.retry,
		header:       header,
		log:          logger,
		transport:    tr,
	}, nil
}

// BaseURL reports the configured API root.
func (c *Client) BaseURL() string { return c.baseURL }

// DefaultModel reports the model used when a call does not override it.
func (c *Client) DefaultModel() string { return c.model }

// authoritativeHeaders are owned by the client and are re-applied after any
// caller-supplied headers are merged, at both the client and the per-call level.
// Callers cannot set, replace or append to them.
var authoritativeHeaders = []string{"Authorization", "Content-Type", "Accept"}

// classifiedSentinels are every sentinel an inner layer may have wrapped before a
// failure reaches classifyTransportError. Anything matching one is returned as
// is, which is what keeps the "exactly one sentinel per error" contract in
// errors.go true. A sentinel produced below that is missing here would be
// silently relabelled as a connection failure.
var classifiedSentinels = []error{
	ErrAborted,
	ErrTimeout,
	ErrConnection,
	ErrDecode,
	ErrValidation,
	ErrAPI,
	ErrRateLimit,
}

// systemOnePayload is the request body. Questions are carried as pre-marshalled
// JSON so that their encoding is owned entirely by marshalQuestions.
type systemOnePayload struct {
	Model     string          `json:"model"`
	State     json.RawMessage `json:"state"`
	Questions json.RawMessage `json:"questions"`
}

// Evaluate sends state and questions to the service and returns the typed answers.
//
// state is marshalled as JSON and passed through untouched: a string, a struct, a
// map, or a slice are all valid. Send only the material the questions need —
// accuracy falls as unrelated content grows.
//
// The returned *Result holds every answer under the name its question was given.
// Read them with Result.Noul, Result.Choice and Result.Score, which report
// whether the name existed and carried the expected type.
func (c *Client) Evaluate(ctx context.Context, state any, questions Questions, opts ...CallOption) (*Result, error) {
	if err := ValidateQuestions(questions); err != nil {
		return nil, err
	}

	call := callConfig{}
	for _, o := range opts {
		if o != nil {
			o(&call)
		}
	}

	model := c.model
	if call.model != "" {
		model = call.model
	}

	// A per-call policy replaces the client's, and is validated here rather than
	// silently truncated: an invalid policy is a programming error, not a
	// condition to degrade around. It reaches the transport on the request, so a
	// concurrent call on the same Client is unaffected.
	policy := c.retry
	if call.retry != nil {
		policy = *call.retry
		if err := validateRetryPolicy(policy); err != nil {
			return nil, err
		}
	}

	stateJSON, err := marshalNoHTMLEscape(state)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot marshal state: %w", ErrValidation, err)
	}
	questionsJSON, err := marshalQuestions(questions)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot marshal questions: %w", ErrValidation, err)
	}
	body, err := marshalNoHTMLEscape(systemOnePayload{
		Model:     model,
		State:     stateJSON,
		Questions: questionsJSON,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: cannot marshal request: %w", ErrValidation, err)
	}

	header := c.header.Clone()
	for k, vs := range call.header {
		for _, v := range vs {
			header.Add(k, v)
		}
	}
	// Authoritative headers are restored from the client's own set last, using
	// Set rather than Add.
	//
	// Add would be a real vulnerability, not a tidiness issue: a caller-supplied
	// Authorization merged in with Add puts two values on the wire, and any proxy
	// or gateway that reads the last one would authenticate with the caller's
	// token instead of this client's. Header.Get cannot see this, because it
	// returns only the first value, so it has to be Values that gets asserted in
	// tests.
	for _, k := range authoritativeHeaders {
		header.Set(k, c.header.Get(k))
	}

	// Keep the caller's context: once a total deadline is layered on top, the two
	// are no longer distinguishable, and telling "the caller gave up" apart from
	// "we ran out of time" is the whole point of ErrAborted versus ErrTimeout.
	callerCtx := ctx
	if c.totalTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.totalTimeout)
		defer cancel()
	}

	// The transport carries the per-attempt timeout and the retry policy; per-call
	// overrides have to reach it here, on the request rather than by mutating the
	// shared transport.
	req := &httpRequest{method: http.MethodPost, path: "/systemone", body: body, header: header}
	if call.timeout > 0 {
		req.attemptTimeout = call.timeout
	}
	if call.retry != nil {
		req.policy = &policy
	}

	c.log.DebugContext(ctx, "systemone: request",
		slog.String("model", model),
		slog.Int("body_bytes", len(body)))

	resp, err := c.transport.do(ctx, req)
	if err != nil {
		return nil, c.classifyTransportError(callerCtx, err)
	}

	c.log.DebugContext(ctx, "systemone: response",
		slog.Int("status", resp.statusCode),
		slog.Int("body_bytes", len(resp.body)))

	if resp.statusCode < 200 || resp.statusCode > 299 {
		return nil, newAPIError(resp.statusCode, resp.header, resp.body)
	}

	result, err := decodeResult(resp.body)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecode, err)
	}
	return result, nil
}

// classifyTransportError maps a raw transport failure onto this package's typed
// errors.
//
// The transport deliberately returns unwrapped errors rather than inventing
// wording of its own, so that "what kind of failure was this" is decided in
// exactly one place and the errors.Is chain underneath stays intact.
func (c *Client) classifyTransportError(callerCtx context.Context, err error) error {
	// Already classified by a lower layer: pass it through rather than nesting one
	// wrapper inside another.
	//
	// ErrDecode belongs in this list. The transport raises it for a response body
	// past MaxResponseBytes, and a host that answered is not a host we failed to
	// reach; without this the fallback below would wrap it in ConnectionError and
	// the error would match two sentinels at once, contradicting the contract in
	// errors.go. Any sentinel a lower layer can produce has to appear here.
	for _, sentinel := range classifiedSentinels {
		if errors.Is(err, sentinel) {
			return err
		}
	}

	// The caller gave up. That outcome belongs to the caller regardless of what
	// the transport happened to report.
	if errors.Is(callerCtx.Err(), context.Canceled) {
		return &AbortError{Err: err}
	}

	// A deadline passed: either the one the caller set, the per-attempt one, or
	// the total budget. net and url errors report this through Timeout rather
	// than by wrapping context.DeadlineExceeded, so both routes are checked.
	timeout := c.timeout
	if c.totalTimeout > 0 {
		timeout = c.totalTimeout
	}
	var timedOut interface{ Timeout() bool }
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(callerCtx.Err(), context.DeadlineExceeded) ||
		(errors.As(err, &timedOut) && timedOut.Timeout()) {
		return &TimeoutError{Err: err, Timeout: timeout}
	}

	return &ConnectionError{Err: err}
}

// validateRetryPolicy rejects values that would misbehave at runtime.
func validateRetryPolicy(p RetryPolicy) error {
	if p.MaxRetries < 0 {
		return fmt.Errorf("%w: MaxRetries must not be negative", ErrValidation)
	}
	if p.InitialBackoff < 0 || p.MaxBackoff < 0 || p.MaxRetryAfter < 0 {
		return fmt.Errorf("%w: backoff durations must not be negative", ErrValidation)
	}
	if p.MaxBackoff > 0 && p.InitialBackoff > p.MaxBackoff {
		return fmt.Errorf("%w: InitialBackoff (%s) exceeds MaxBackoff (%s)", ErrValidation, p.InitialBackoff, p.MaxBackoff)
	}
	if p.BackoffJitter < 0 || p.BackoffJitter > 1 {
		return fmt.Errorf("%w: BackoffJitter must be within [0,1], got %v", ErrValidation, p.BackoffJitter)
	}
	return nil
}
