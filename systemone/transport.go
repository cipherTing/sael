package systemone

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"
)

// MaxResponseBytes bounds how much of a response body this package will buffer.
// A response larger than this is an error rather than an attempt to hold it in
// memory: the endpoint returns a small JSON document, so a body past this size
// means something is wrong with the host, not that a bigger answer arrived.
//
// The bound exists to turn a misbehaving host — one that streams endlessly, for
// example — into a prompt error instead of an out-of-memory kill. 32 MiB leaves
// enormous headroom over the few hundred bytes a real answer occupies.
//
// Exceeding it is reported as an error wrapping ErrDecode (the body could not
// be interpreted because it could not be read to its end), and is never
// retried: retrying would buffer another limit's worth of memory before failing
// again.
const MaxResponseBytes = 32 << 20 // 32 MiB

// httpRequest is one logical request: method, path relative to the transport's
// base URL, raw body and headers.
//
// attemptTimeout, when positive, overrides the transport-wide per-attempt
// timeout for this request only (it carries WithCallTimeout from the client).
type httpRequest struct {
	method string
	path   string
	body   []byte
	header http.Header

	// attemptTimeout overrides the transport's per-attempt timeout when > 0.
	attemptTimeout time.Duration

	// policy overrides the transport's retry policy for this request when
	// non-nil. It is carried on the request rather than written back onto the
	// transport, because one transport is shared by concurrent calls and a
	// per-call override must not race with them.
	policy *RetryPolicy
}

// httpResponse is a fully read response. HTTP failures (4xx/5xx) are reported
// here rather than as an error; the transport only returns an error when it
// could not obtain a response at all.
type httpResponse struct {
	statusCode int
	header     http.Header
	body       []byte
}

// transport performs the HTTP conversation for the System One endpoint: it
// builds the URL, applies the per-attempt deadline, reads and closes the body,
// and retries according to a RetryPolicy.
//
// It deals exclusively in []byte and never interprets the payload, so the
// JSON layer above it stays the only place that knows the wire schema.
//
// # Logging
//
// The logger may be nil, in which case everything is discarded. Requests are
// logged at debug level including the raw request body, mirroring the official
// SDK; header values are never logged, so the Authorization credential cannot
// leak through this layer. Failures and retries are logged at warn level with
// the error and the elapsed time.
type transport struct {
	baseURL string
	hc      *http.Client
	timeout time.Duration
	policy  RetryPolicy
	log     *slog.Logger

	// maxResponseBytes overrides MaxResponseBytes when positive. It exists so
	// tests can exercise the limit without buffering 32 MiB; production code
	// leaves it zero and gets the exported constant.
	maxResponseBytes int64
}

// newTransport builds a transport. baseURL may carry a trailing slash (it is
// trimmed); hc may be nil (http.DefaultClient is used); log may be nil (output
// is discarded).
func newTransport(baseURL string, hc *http.Client, timeout time.Duration, rp RetryPolicy, log *slog.Logger) *transport {
	if hc == nil {
		hc = http.DefaultClient
	}
	if log == nil {
		log = discardLogger()
	}
	return &transport{
		baseURL: strings.TrimRight(baseURL, "/"),
		hc:      hc,
		timeout: timeout,
		policy:  rp,
		log:     log,
	}
}

// discardLogger returns a logger that throws everything away.
func discardLogger() *slog.Logger {
	return slog.New(discardHandler{})
}

// discardHandler is a slog.Handler that writes nothing. It exists so the
// package builds against go1.23 (slog.DiscardHandler requires go1.24, which
// would make "go vet" fail on the module's go directive).
type discardHandler struct{}

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (d discardHandler) WithAttrs([]slog.Attr) slog.Handler      { return d }
func (d discardHandler) WithGroup(string) slog.Handler           { return d }

// do performs req, retrying as the policy allows, and returns the last
// response.
//
// Behaviour:
//
//   - The URL is baseURL (trailing slashes trimmed) + path.
//   - Each attempt gets its own context.WithTimeout, nested inside the caller's
//     context, so either deadline aborts the attempt.
//   - A retryable status code, or a transport-level error, triggers a retry
//     while attempts remain: MaxRetries retries after the first attempt.
//   - Retry-After (seconds or HTTP-date) is honoured when RespectRetryAfter is
//     set and it does not exceed MaxRetryAfter; otherwise the exponential
//     backoff is used.
//   - Cancellation of the caller's context returns immediately, without
//     retrying.
//   - A body larger than MaxResponseBytes fails the call with an error wrapping
//     ErrDecode instead of being buffered; see MaxResponseBytes.
//   - A non-2xx status is not an error: the response is returned untouched and
//     the JSON layer maps it to a typed error. An error is returned only when
//     no response could be obtained (connection failure or timeout, retries
//     exhausted) or when the caller cancelled.
func (t *transport) do(ctx context.Context, req *httpRequest) (*httpResponse, error) {
	if req == nil {
		return nil, errors.New("systemone: transport: nil request")
	}

	url := t.baseURL + canonicalPath(req.path)
	policy := t.effectivePolicy(req)
	attempts := policy.maxAttempts()
	sequence := t.newBackoffSequence(policy)

	// Header of the response that triggered the attempt about to be made; it
	// carries Retry-After. Nil when the previous attempt failed without a
	// response.
	var lastHeader http.Header

	for attempt := 1; ; attempt++ {
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}

		if attempt > 1 {
			delay := t.retryDelay(policy, sequence, lastHeader)
			if t.log.Enabled(ctx, slog.LevelDebug) {
				t.log.DebugContext(ctx, "systemone: backing off before retry",
					"attempt", attempt, "delay", delay.String())
			}
			if err := waitForRetry(ctx, delay); err != nil {
				return nil, err
			}
		}

		resp, err := t.attempt(ctx, req, url, attempt)
		lastHeader = nil

		if err == nil {
			if attempt >= attempts || !policy.retryableStatus(resp.statusCode) {
				return resp, nil
			}
			lastHeader = resp.header
			if t.log.Enabled(ctx, slog.LevelWarn) {
				t.log.WarnContext(ctx, "systemone: retrying after HTTP status",
					"status", resp.statusCode, "attempt", attempt, "max_attempts", attempts)
			}
			continue
		}

		// The caller's context decided the outcome: surface it and do not retry.
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		if attempt >= attempts || !t.retryableError(policy, err) {
			return nil, err
		}
		if t.log.Enabled(ctx, slog.LevelWarn) {
			t.log.WarnContext(ctx, "systemone: retrying after transport error",
				"attempt", attempt, "max_attempts", attempts, "error", err)
		}
	}
}

// effectivePolicy is the per-call override when one was supplied, else the
// transport-wide policy.
func (t *transport) effectivePolicy(req *httpRequest) RetryPolicy {
	if req.policy != nil {
		return *req.policy
	}
	return t.policy
}

// attempt performs a single HTTP round trip and reads the whole body, at most
// responseLimit() bytes of it.
//
// The response body is closed on every path, retried attempts included, so a
// discarded retry cannot leak a connection. That holds for the oversized-body
// failure too: the deferred Close runs before the error is returned.
func (t *transport) attempt(ctx context.Context, req *httpRequest, url string, attempt int) (*httpResponse, error) {
	attemptCtx := ctx
	if timeout := t.attemptTimeout(req); timeout > 0 {
		var cancel context.CancelFunc
		attemptCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	var body io.Reader
	if len(req.body) > 0 {
		body = bytes.NewReader(req.body)
	}
	hr, err := http.NewRequestWithContext(attemptCtx, req.method, url, body)
	if err != nil {
		return nil, err
	}
	if req.header != nil {
		hr.Header = req.header.Clone()
	}
	if len(req.body) > 0 && hr.Header.Get("Content-Type") == "" {
		hr.Header.Set("Content-Type", "application/json")
	}

	if t.log.Enabled(ctx, slog.LevelDebug) {
		// Only the body is logged; header values (notably Authorization) are
		// deliberately never passed to the logger.
		t.log.DebugContext(ctx, "systemone: sending request",
			"method", req.method,
			"url", url,
			"attempt", attempt,
			"body", string(req.body))
	}

	started := time.Now()
	resp, err := t.hc.Do(hr)
	if err != nil {
		t.logAttemptFailure(ctx, req, attempt, time.Since(started), err)
		return nil, err
	}
	defer resp.Body.Close()

	// Read one byte past the limit so that a body of exactly the limit still
	// succeeds while anything larger is detected without buffering it all.
	limit := t.responseLimit()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	elapsed := time.Since(started)
	if err != nil {
		t.logAttemptFailure(ctx, req, attempt, elapsed, err)
		return nil, err
	}
	if int64(len(data)) > limit {
		err := fmt.Errorf("%w: response body exceeds MaxResponseBytes (%d bytes)", ErrDecode, limit)
		t.logAttemptFailure(ctx, req, attempt, elapsed, err)
		return nil, err
	}

	if t.log.Enabled(ctx, slog.LevelDebug) {
		t.log.DebugContext(ctx, "systemone: received response",
			"method", req.method,
			"url", url,
			"attempt", attempt,
			"status", resp.StatusCode,
			"body_bytes", len(data),
			"body", string(data),
			"duration", elapsed.String())
	}

	return &httpResponse{
		statusCode: resp.StatusCode,
		header:     resp.Header.Clone(),
		body:       data,
	}, nil
}

// attemptTimeout reports the per-attempt deadline for req: the per-call
// override when set, else the transport default.
func (t *transport) attemptTimeout(req *httpRequest) time.Duration {
	if req.attemptTimeout > 0 {
		return req.attemptTimeout
	}
	return t.timeout
}

// responseLimit is how many body bytes this transport buffers: MaxResponseBytes
// unless a test shrank it.
func (t *transport) responseLimit() int64 {
	if t.maxResponseBytes > 0 {
		return t.maxResponseBytes
	}
	return MaxResponseBytes
}

// retryableError reports whether a transport-level error should be retried.
//
// Caller cancellation is never retried (the caller asked to stop), and neither
// is an oversized body (ErrDecode): the size is a property of the host, not a
// transient fault, and a retry would buffer another limit's worth of memory
// before failing again. Timeouts are governed by RetryTimeoutErrors; everything
// else — dial failures, resets, truncated bodies — by RetryConnectionErrors.
func (t *transport) retryableError(policy RetryPolicy, err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, ErrDecode) {
		return false
	}
	if isTimeoutError(err) {
		return policy.RetryTimeoutErrors
	}
	return policy.RetryConnectionErrors
}

// isTimeoutError reports whether err is a deadline or timeout failure.
func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}

// newBackoffSequence builds the exponential sequence for one do call:
// InitialBackoff doubling up to MaxBackoff. The randomness of
// backoff.ExponentialBackOff is disabled (RandomizationFactor 0) because the
// frozen policy describes one-sided jitter — a subtraction of up to
// BackoffJitter — while the library randomizes symmetrically (±factor); adding
// both would double-count and could exceed MaxBackoff. The jitter is applied in
// retryDelay.
func (t *transport) newBackoffSequence(policy RetryPolicy) *backoff.ExponentialBackOff {
	maxInterval := policy.MaxBackoff
	if maxInterval <= 0 {
		// Unbounded: a value large enough that the exponential never reaches it
		// within any realistic retry count.
		maxInterval = time.Duration(1) << 62
	}
	sequence := &backoff.ExponentialBackOff{
		InitialInterval:     policy.InitialBackoff,
		RandomizationFactor: 0,
		Multiplier:          2,
		MaxInterval:         maxInterval,
	}
	sequence.Reset()
	return sequence
}

// retryDelay returns how long to wait before the next attempt, given the
// backoff sequence and the header of the response that triggered the retry.
//
// A usable Retry-After wins over the computed backoff, unless it exceeds
// MaxRetryAfter — then the backoff is used instead.
func (t *transport) retryDelay(policy RetryPolicy, sequence *backoff.ExponentialBackOff, lastHeader http.Header) time.Duration {
	if policy.RespectRetryAfter {
		if after, ok := parseRetryAfter(lastHeader); ok {
			if policy.MaxRetryAfter <= 0 || after <= policy.MaxRetryAfter {
				return clampDuration(after)
			}
			if t.log.Enabled(context.Background(), slog.LevelDebug) {
				t.log.Debug("systemone: ignoring Retry-After beyond MaxRetryAfter",
					"retry_after", after.String(), "max", policy.MaxRetryAfter.String())
			}
		}
	}

	delay := sequence.NextBackOff()
	if max := policy.MaxBackoff; max > 0 && delay > max {
		// Defensive: keep the first interval inside the cap even if the policy
		// was built with InitialBackoff > MaxBackoff.
		delay = max
	}
	delay = clampDuration(delay)

	if jitter := policy.BackoffJitter; jitter > 0 {
		if jitter > 1 {
			jitter = 1
		}
		delay = clampDuration(time.Duration(float64(delay) * (1 - jitter*rand.Float64())))
	}
	return delay
}

// logAttemptFailure logs a failed attempt at warn level without touching header
// values.
func (t *transport) logAttemptFailure(ctx context.Context, req *httpRequest, attempt int, elapsed time.Duration, err error) {
	if !t.log.Enabled(ctx, slog.LevelWarn) {
		return
	}
	t.log.WarnContext(ctx, "systemone: request attempt failed",
		"method", req.method,
		"attempt", attempt,
		"duration", elapsed.String(),
		"error", err)
}

// clampDuration floors a duration at zero.
func clampDuration(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

// waitForRetry sleeps for delay, returning early if ctx is cancelled.
func waitForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return context.Cause(ctx)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}

// canonicalPath makes path absolute so it can be appended to the trimmed base
// URL without producing a doubled or missing slash.
func canonicalPath(path string) string {
	if path == "" || strings.HasPrefix(path, "/") {
		return path
	}
	return "/" + path
}
