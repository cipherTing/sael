package systemone

import (
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// Defaults applied when neither an Option nor an environment variable supplies a
// value.
const (
	// DefaultBaseURL is the official System One API root. The endpoint is
	// {BaseURL}/systemone.
	//
	// Nothing about a particular gateway is baked in. Point BaseURL at any host
	// that speaks the same API shape — a gateway, a proxy, or a test server —
	// and everything else stays the same. Gateways may add fields to the
	// response (see Result.ID, Result.Provider, Usage.Cost); those are decoded
	// when present and left zero when absent.
	DefaultBaseURL = "https://api.typesafe.ai/v1"

	// DefaultModel is the Jev model id in the API's own naming. Hosts may
	// namespace it with their own prefix, so this is frequently overridden.
	DefaultModel = "jev-latest"

	// DefaultTimeout bounds a single attempt, not the whole call. This matches
	// the official SDK's 10s default; see WithTotalTimeout for an overall bound.
	DefaultTimeout = 10 * time.Second
)

// Environment variables consulted by New. Explicit Options always win, then the
// environment, then the defaults above. The names match the official SDKs.
const (
	EnvAPIKey  = "TYPESAFE_API_KEY"
	EnvBaseURL = "TYPESAFE_BASE_URL"
	EnvModel   = "TYPESAFE_DEFAULT_MODEL"
)

type config struct {
	apiKey           string
	baseURL          string
	model            string
	httpClient       *http.Client
	timeout          time.Duration
	totalTimeout     time.Duration
	maxResponseBytes int64
	retry            RetryPolicy
	header           http.Header
	logger           *slog.Logger
}

// defaultConfig leaves baseURL and model empty so that New can tell "the caller
// did not choose" apart from "the caller chose the default", which is what makes
// the Option > environment > default precedence work.
func defaultConfig() *config {
	return &config{
		timeout: DefaultTimeout,
		retry:   DefaultRetryPolicy(),
		header:  make(http.Header),
	}
}

// An Option configures a Client.
type Option func(*config)

// WithAPIKey sets the bearer token. When unset, New reads EnvAPIKey.
func WithAPIKey(key string) Option {
	return func(c *config) { c.apiKey = key }
}

// WithBaseURL sets the API root; the client appends "/systemone". Use this to
// target a gateway, a proxy, or a local test server.
func WithBaseURL(url string) Option {
	return func(c *config) { c.baseURL = strings.TrimSpace(url) }
}

// WithModel sets the default model id, overridable per call with WithCallModel.
func WithModel(model string) Option {
	return func(c *config) { c.model = strings.TrimSpace(model) }
}

// WithHTTPClient supplies the underlying client.
//
// The default client sets no Timeout, because deadlines are applied per attempt
// through the context, and refuses to follow redirects: this endpoint is a single
// POST to a fixed URL, and following a 3xx silently rewrites the request into a
// GET without a body while replaying credentials at a location the server chose.
// Supplying a client here replaces that policy with whatever it carries, so a
// caller who passes one takes over responsibility for redirect handling.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *config) { c.httpClient = hc }
}

// WithTimeout bounds a single attempt. Zero disables the per-attempt deadline.
func WithTimeout(d time.Duration) Option {
	return func(c *config) { c.timeout = d }
}

// WithTotalTimeout bounds the whole call, retries included.
//
// The official SDK has no equivalent: its timeout is per attempt, so three
// attempts of a 10s timeout can stall a caller for 30s or more. On a request
// path that is usually unacceptable, so set this. Zero means no overall bound.
func WithTotalTimeout(d time.Duration) Option {
	return func(c *config) { c.totalTimeout = d }
}

// WithRetryPolicy replaces the retry policy. Use DefaultRetryPolicy() as a base.
func WithRetryPolicy(p RetryPolicy) Option {
	return func(c *config) { c.retry = p }
}

// WithMaxResponseBytes bounds how much of a response body is buffered before the
// call fails. Zero or negative means MaxResponseBytes.
//
// The bound exists so that a host which keeps sending cannot exhaust memory. It
// is not a rate limit, and the real responses are kilobytes, so lowering it is
// only worth doing if a caller wants to be stricter than the default.
func WithMaxResponseBytes(n int64) Option {
	return func(c *config) { c.maxResponseBytes = n }
}

// WithHeader adds a header sent on every request, for caller-specific needs such
// as gateway attribution or a tracing id. It is a general escape hatch, not an
// accommodation for any particular host: this API ignores unknown request
// headers, so most callers never need it. Headers added here cannot override
// Authorization, Content-Type or Accept.
func WithHeader(key, value string) Option {
	return func(c *config) { c.header.Set(key, value) }
}

// WithLogger sets a structured logger. The default discards all output.
//
// Credential headers are never logged. Request and response bodies are logged at
// debug level when a logger is supplied, which is the same trade-off the
// official SDK makes; do not enable debug logging on untrusted content.
func WithLogger(l *slog.Logger) Option {
	return func(c *config) { c.logger = l }
}

// callConfig holds per-call overrides.
type callConfig struct {
	model   string
	timeout time.Duration
	retry   *RetryPolicy
	header  http.Header
}

// A CallOption overrides client settings for one call.
type CallOption func(*callConfig)

// WithCallModel overrides the model for one call.
func WithCallModel(model string) CallOption {
	return func(c *callConfig) { c.model = model }
}

// WithCallTimeout overrides the per-attempt timeout for one call.
func WithCallTimeout(d time.Duration) CallOption {
	return func(c *callConfig) { c.timeout = d }
}

// WithCallRetryPolicy overrides the retry policy for one call.
func WithCallRetryPolicy(p RetryPolicy) CallOption {
	return func(c *callConfig) { c.retry = &p }
}

// WithCallHeaders adds headers for one call. They cannot override Authorization,
// Content-Type or Accept.
func WithCallHeaders(h http.Header) CallOption {
	return func(c *callConfig) {
		if c.header == nil {
			c.header = make(http.Header)
		}
		for k, vs := range h {
			for _, v := range vs {
				c.header.Add(k, v)
			}
		}
	}
}

// envOr returns the first non-empty environment variable among names.
func envOr(names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}
	return ""
}

// applyOptions runs every non-nil Option against cfg. Option values are plain
// setters, so this is safe to call more than once.
func applyOptions(cfg *config, opts []Option) {
	for _, o := range opts {
		if o != nil {
			o(cfg)
		}
	}
}

// applyEnv overlays environment variables.
//
// It runs after the configuration directory and before explicit Options, so an
// exported variable beats a file on disk but never beats code. Values are only
// assigned when the variable is set, which is what lets the layer below show
// through.
func applyEnv(cfg *config) {
	if v := envOr(EnvAPIKey); v != "" {
		cfg.apiKey = v
	}
	if v := envOr(EnvBaseURL); v != "" {
		cfg.baseURL = v
	}
	if v := envOr(EnvModel); v != "" {
		cfg.model = v
	}
}
