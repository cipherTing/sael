package systemone

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// test helpers (shared with retry_test.go)
// ---------------------------------------------------------------------------

// retryTestPolicy builds a policy with deterministic (jitter-free) timing and a
// small retryable status list, so tests finish in milliseconds.
func retryTestPolicy(maxRetries int, initial, max time.Duration) RetryPolicy {
	return RetryPolicy{
		MaxRetries:            maxRetries,
		InitialBackoff:        initial,
		MaxBackoff:            max,
		BackoffJitter:         0,
		RetryStatuses:         []int{408, 429, 500, 502, 503, 504, 599},
		RespectRetryAfter:     false,
		MaxRetryAfter:         time.Minute,
		RetryConnectionErrors: true,
		RetryTimeoutErrors:    true,
	}
}

type capturedRequest struct {
	method      string
	path        string
	contentType string
	auth        string
	body        []byte
	at          time.Time
}

// requestCapture records what the server actually received. It is mutex-guarded
// because the handler runs on its own goroutine while the test asserts from the
// test goroutine.
type requestCapture struct {
	mu       sync.Mutex
	requests []capturedRequest
}

// record stores r and returns its 1-based ordinal.
func (c *requestCapture) record(r *http.Request) int {
	body, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, capturedRequest{
		method:      r.Method,
		path:        r.URL.Path,
		contentType: r.Header.Get("Content-Type"),
		auth:        r.Header.Get("Authorization"),
		body:        body,
		at:          time.Now(),
	})
	return len(c.requests)
}

func (c *requestCapture) all() []capturedRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]capturedRequest, len(c.requests))
	copy(out, c.requests)
	return out
}

func (c *requestCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

// stubRoundTripper fails every attempt with a fixed error, counting calls.
// It lets the error-classification paths be tested without any network.
type stubRoundTripper struct {
	calls atomic.Int32
	err   error
}

func (s *stubRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	s.calls.Add(1)
	return nil, s.err
}

// countingBody counts Close calls so the test can prove that every attempt
// releases its response body, discarded retries included.
type countingBody struct {
	reader io.Reader
	closes atomic.Int32
}

func (b *countingBody) Read(p []byte) (int, error) { return b.reader.Read(p) }

func (b *countingBody) Close() error {
	b.closes.Add(1)
	return nil
}

// bodyRecordingRoundTripper answers every request with a fresh body whose Close
// is counted. payload defaults to a small JSON error document.
type bodyRecordingRoundTripper struct {
	mu      sync.Mutex
	bodies  []*countingBody
	status  int
	payload []byte
}

func (rt *bodyRecordingRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	payload := rt.payload
	if payload == nil {
		payload = []byte(`{"error":{"message":"boom"}}`)
	}
	body := &countingBody{reader: bytes.NewReader(payload)}
	rt.mu.Lock()
	rt.bodies = append(rt.bodies, body)
	rt.mu.Unlock()
	return &http.Response{
		StatusCode:    rt.status,
		Header:        make(http.Header),
		Body:          body,
		ContentLength: -1,
		Request:       r,
	}, nil
}

func (rt *bodyRecordingRoundTripper) recorded() []*countingBody {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	out := make([]*countingBody, len(rt.bodies))
	copy(out, rt.bodies)
	return out
}

// newEchoServer answers with tc.status, recording every request.
func newEchoServer(status int) (*httptest.Server, *requestCapture) {
	capture := &requestCapture{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.record(r)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"message":"boom","code":` + strconv.Itoa(status) + `}}`))
	})), capture
}

// ---------------------------------------------------------------------------
// request construction
// ---------------------------------------------------------------------------

func TestTransportURLJoining(t *testing.T) {
	tests := []struct {
		name     string
		trailing string
	}{
		{"no trailing slash", ""},
		{"single trailing slash", "/"},
		{"several trailing slashes", "///"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, capture := newEchoServer(http.StatusOK)
			defer srv.Close()

			tr := newTransport(srv.URL+tc.trailing, srv.Client(), time.Second,
				retryTestPolicy(0, time.Millisecond, 0), nil)

			resp, err := tr.do(context.Background(), &httpRequest{
				method: http.MethodPost,
				path:   "/systemone",
				body:   []byte(`{"model":"m"}`),
				header: http.Header{"Authorization": {"Bearer secret"}},
			})
			require.NoError(t, err)
			require.NotNil(t, resp)
			assert.Equal(t, http.StatusOK, resp.statusCode)
			assert.Equal(t, `{"error":{"message":"boom","code":200}}`, string(resp.body))

			require.Len(t, capture.all(), 1)
			got := capture.all()[0]
			assert.Equal(t, "/systemone", got.path, "base URL slash handling must not double or drop the separator")
			assert.Equal(t, http.MethodPost, got.method)
			assert.Equal(t, "Bearer secret", got.auth)
			assert.Equal(t, "application/json", got.contentType)
			assert.Equal(t, `{"model":"m"}`, string(got.body))
		})
	}
}

func TestTransportNilHTTPClientAndNilLogger(t *testing.T) {
	srv, _ := newEchoServer(http.StatusOK)
	defer srv.Close()

	tr := newTransport(srv.URL, nil, time.Second, retryTestPolicy(0, time.Millisecond, 0), nil)
	resp, err := tr.do(context.Background(), &httpRequest{
		method: http.MethodPost,
		path:   "/systemone",
		body:   []byte(`{}`),
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusOK, resp.statusCode)
}

// ---------------------------------------------------------------------------
// retry decisions
// ---------------------------------------------------------------------------

func TestTransportRetryStatusTable(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		maxRetries   int
		wantAttempts int
	}{
		{"408 is retried", http.StatusRequestTimeout, 2, 3},
		{"429 is retried", http.StatusTooManyRequests, 2, 3},
		{"500 is retried", http.StatusInternalServerError, 2, 3},
		{"502 is retried", http.StatusBadGateway, 2, 3},
		{"503 is retried", http.StatusServiceUnavailable, 2, 3},
		{"504 is retried", http.StatusGatewayTimeout, 2, 3},
		{"599 is retried", 599, 2, 3},
		{"400 is not retried", http.StatusBadRequest, 2, 1},
		{"401 is not retried", http.StatusUnauthorized, 2, 1},
		{"404 is not retried", http.StatusNotFound, 2, 1},
		{"422 is not retried", http.StatusUnprocessableEntity, 2, 1},
		{"200 is not retried", http.StatusOK, 2, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, capture := newEchoServer(tc.status)
			defer srv.Close()

			tr := newTransport(srv.URL, srv.Client(), time.Second,
				retryTestPolicy(tc.maxRetries, time.Millisecond, time.Millisecond), nil)

			resp, err := tr.do(context.Background(), &httpRequest{
				method: http.MethodPost, path: "/systemone", body: []byte(`{}`),
			})

			// HTTP failures are never transport errors: client.go maps them.
			require.NoError(t, err)
			require.NotNil(t, resp)
			assert.Equal(t, tc.status, resp.statusCode)
			assert.Equal(t, `{"error":{"message":"boom","code":`+strconv.Itoa(tc.status)+`}}`, string(resp.body))
			assert.Equal(t, tc.wantAttempts, capture.count())
		})
	}
}

func TestTransportRetryAttemptLimit(t *testing.T) {
	tests := []struct {
		name         string
		maxRetries   int
		wantAttempts int
	}{
		{"no retries", 0, 1},
		{"two retries", 2, 3},
		{"five retries", 5, 6},
		{"negative retries clamp to one attempt", -3, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, capture := newEchoServer(http.StatusInternalServerError)
			defer srv.Close()

			tr := newTransport(srv.URL, srv.Client(), time.Second,
				retryTestPolicy(tc.maxRetries, time.Millisecond, time.Millisecond), nil)

			resp, err := tr.do(context.Background(), &httpRequest{
				method: http.MethodPost, path: "/systemone", body: []byte(`{}`),
			})

			require.NoError(t, err)
			require.NotNil(t, resp)
			assert.Equal(t, http.StatusInternalServerError, resp.statusCode)
			assert.Equal(t, tc.wantAttempts, capture.count(),
				"MaxRetries retries after the first attempt means MaxRetries+1 total attempts")
		})
	}
}

func TestTransportBackoffGrowsWithAttempts(t *testing.T) {
	srv, capture := newEchoServer(http.StatusInternalServerError)
	defer srv.Close()

	// 10ms doubling: 10, 20, 40, 80.
	tr := newTransport(srv.URL, srv.Client(), time.Second,
		retryTestPolicy(4, 10*time.Millisecond, time.Second), nil)

	start := time.Now()
	resp, err := tr.do(context.Background(), &httpRequest{
		method: http.MethodPost, path: "/systemone", body: []byte(`{}`),
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 5, capture.count())
	assert.Less(t, elapsed, 2*time.Second, "test must stay in the millisecond range")

	requests := capture.all()
	gaps := make([]time.Duration, 0, len(requests)-1)
	for i := 1; i < len(requests); i++ {
		gaps = append(gaps, requests[i].at.Sub(requests[i-1].at))
	}
	require.Len(t, gaps, 4)
	for i := 1; i < len(gaps); i++ {
		assert.Greater(t, gaps[i], gaps[i-1],
			"backoff must grow: gap %d (%s) should exceed gap %d (%s)", i, gaps[i], i-1, gaps[i-1])
	}
}

func TestTransportRetryAfterSecondsHonoured(t *testing.T) {
	capture := &requestCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture.record(r) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	// The backoff is 5s: honouring "Retry-After: 1" is observable as ~1s total.
	policy := retryTestPolicy(2, 5*time.Second, 5*time.Second)
	policy.RespectRetryAfter = true
	policy.MaxRetryAfter = 60 * time.Second
	tr := newTransport(srv.URL, srv.Client(), 5*time.Second, policy, nil)

	start := time.Now()
	resp, err := tr.do(context.Background(), &httpRequest{
		method: http.MethodPost, path: "/systemone", body: []byte(`{}`),
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusOK, resp.statusCode)
	assert.Equal(t, 2, capture.count())
	assert.GreaterOrEqual(t, elapsed, time.Second, "Retry-After: 1 must be waited out")
	assert.Less(t, elapsed, 4*time.Second, "the 5s backoff must have been replaced, not added")
}

func TestTransportRetryAfterHTTPDateHonoured(t *testing.T) {
	capture := &requestCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture.record(r) == 1 {
			// A date in the past means "retry now": the wait collapses to zero,
			// which is observable because the backoff would have been 5s.
			w.Header().Set("Retry-After", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	policy := retryTestPolicy(2, 5*time.Second, 5*time.Second)
	policy.RespectRetryAfter = true
	policy.MaxRetryAfter = 60 * time.Second
	tr := newTransport(srv.URL, srv.Client(), 5*time.Second, policy, nil)

	start := time.Now()
	resp, err := tr.do(context.Background(), &httpRequest{
		method: http.MethodPost, path: "/systemone", body: []byte(`{}`),
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusOK, resp.statusCode)
	assert.Equal(t, 2, capture.count())
	assert.Less(t, elapsed, 2*time.Second, "an HTTP-date Retry-After must be parsed, not ignored in favour of the 5s backoff")
}

func TestTransportRetryAfterIgnoredWhenNotRespected(t *testing.T) {
	capture := &requestCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture.record(r) == 1 {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	policy := retryTestPolicy(2, 5*time.Second, 5*time.Second)
	policy.RespectRetryAfter = false
	tr := newTransport(srv.URL, srv.Client(), 5*time.Second, policy, nil)

	// retryDelay is asserted directly: actually sleeping would take 5s.
	delay := tr.retryDelay(tr.policy, tr.newBackoffSequence(tr.policy), http.Header{"Retry-After": {"120"}})
	assert.Equal(t, 5*time.Second, delay)
}

// ---------------------------------------------------------------------------
// cancellation and timeouts
// ---------------------------------------------------------------------------

func TestTransportCancelDuringBackoffStopsRetrying(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, capture := newEchoServer(http.StatusInternalServerError)
	defer srv.Close()

	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.record(r)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{}`))
		cancel() // cancel while the transport is about to back off
	})

	policy := retryTestPolicy(5, 30*time.Second, 30*time.Second)
	tr := newTransport(srv.URL, srv.Client(), 5*time.Second, policy, nil)

	start := time.Now()
	resp, err := tr.do(ctx, &httpRequest{method: http.MethodPost, path: "/systemone", body: []byte(`{}`)})
	elapsed := time.Since(start)

	assert.Nil(t, resp)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, capture.count(), "a cancelled context must not be retried")
	assert.Less(t, elapsed, 2*time.Second, "cancellation must interrupt the 30s backoff")
}

func TestTransportCanceledContextMakesNoRequest(t *testing.T) {
	srv, capture := newEchoServer(http.StatusOK)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tr := newTransport(srv.URL, srv.Client(), time.Second, retryTestPolicy(2, time.Millisecond, time.Millisecond), nil)
	resp, err := tr.do(ctx, &httpRequest{method: http.MethodPost, path: "/systemone", body: []byte(`{}`)})

	assert.Nil(t, resp)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, capture.count())
}

func TestTransportAttemptTimeoutIsRetriedPerPolicy(t *testing.T) {
	tests := []struct {
		name               string
		retryTimeoutErrors bool
		wantAttempts       int
	}{
		{"timeouts are retried", true, 3},
		{"timeouts are not retried", false, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			// Handlers park on release instead of sleeping, so the test costs
			// only the per-attempt deadlines. close(release) runs before
			// srv.Close() (LIFO defers), letting the handlers finish.
			release := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				<-release // never answer: the per-attempt deadline ends the attempt
			}))
			defer srv.Close()
			defer close(release)

			policy := retryTestPolicy(2, time.Millisecond, time.Millisecond)
			policy.RetryTimeoutErrors = tc.retryTimeoutErrors
			tr := newTransport(srv.URL, srv.Client(), 20*time.Millisecond, policy, nil)

			start := time.Now()
			resp, err := tr.do(context.Background(), &httpRequest{
				method: http.MethodPost, path: "/systemone", body: []byte(`{}`),
			})
			elapsed := time.Since(start)

			assert.Nil(t, resp)
			require.Error(t, err)
			assert.True(t, isTimeoutError(err), "want a timeout error, got %v", err)
			assert.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Equal(t, int32(tc.wantAttempts), calls.Load(),
				"the timeout must be retried only when RetryTimeoutErrors is set")
			assert.Less(t, elapsed, 2*time.Second)
		})
	}
}

func TestTransportAttemptTimeoutOverrideIsUsed(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-release // never answer: the per-attempt deadline ends the attempt
	}))
	defer srv.Close()
	defer close(release)

	// Transport default is generous; the per-request override is what times out.
	policy := retryTestPolicy(0, time.Millisecond, time.Millisecond)
	tr := newTransport(srv.URL, srv.Client(), 30*time.Second, policy, nil)

	start := time.Now()
	_, err := tr.do(context.Background(), &httpRequest{
		method: http.MethodPost, path: "/systemone", body: []byte(`{}`),
		attemptTimeout: 20 * time.Millisecond,
	})

	require.Error(t, err)
	assert.True(t, isTimeoutError(err), "want a timeout error, got %v", err)
	assert.Less(t, time.Since(start), 2*time.Second)
	assert.Equal(t, int32(1), calls.Load())
}

// ---------------------------------------------------------------------------
// transport-level errors
// ---------------------------------------------------------------------------

func TestTransportConnectionErrorRetryClassification(t *testing.T) {
	connectionErr := &net.OpError{Op: "dial", Net: "tcp", Err: errString("connection refused")}

	tests := []struct {
		name                  string
		err                   error
		retryConnectionErrors bool
		retryTimeoutErrors    bool
		wantAttempts          int
	}{
		{"connection error retried", connectionErr, true, true, 3},
		{"connection error not retried", connectionErr, false, true, 1},
		{"timeout retried as timeout", context.DeadlineExceeded, false, true, 3},
		{"timeout not retried", context.DeadlineExceeded, true, false, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubRoundTripper{err: tc.err}
			policy := retryTestPolicy(2, time.Millisecond, time.Millisecond)
			policy.RetryConnectionErrors = tc.retryConnectionErrors
			policy.RetryTimeoutErrors = tc.retryTimeoutErrors
			tr := newTransport("http://example.invalid", &http.Client{Transport: stub}, time.Second, policy, nil)

			resp, err := tr.do(context.Background(), &httpRequest{
				method: http.MethodPost, path: "/systemone", body: []byte(`{}`),
			})

			assert.Nil(t, resp)
			require.Error(t, err)
			assert.Equal(t, tc.wantAttempts, int(stub.calls.Load()))
		})
	}
}

func TestTransportExhaustedConnectionErrorIsReturned(t *testing.T) {
	stub := &stubRoundTripper{err: &net.OpError{Op: "dial", Net: "tcp", Err: errString("connection refused")}}
	tr := newTransport("http://example.invalid", &http.Client{Transport: stub}, time.Second,
		retryTestPolicy(2, time.Millisecond, time.Millisecond), nil)

	resp, err := tr.do(context.Background(), &httpRequest{
		method: http.MethodPost, path: "/systemone", body: []byte(`{}`),
	})

	assert.Nil(t, resp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused", "the underlying cause must survive")
	assert.Equal(t, 3, int(stub.calls.Load()))
}

// errString is a tiny error type whose message is stable.
type errString string

func (e errString) Error() string { return string(e) }

// ---------------------------------------------------------------------------
// body handling
// ---------------------------------------------------------------------------

func TestTransportClosesResponseBodyOnEveryAttempt(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		maxRetries   int
		wantAttempts int
	}{
		{"retried attempts close their body", http.StatusInternalServerError, 2, 3},
		{"non-retryable failure closes its body", http.StatusBadRequest, 2, 1},
		{"success closes its body", http.StatusOK, 2, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rt := &bodyRecordingRoundTripper{status: tc.status}
			tr := newTransport("http://example.invalid", &http.Client{Transport: rt}, time.Second,
				retryTestPolicy(tc.maxRetries, time.Millisecond, time.Millisecond), nil)

			resp, err := tr.do(context.Background(), &httpRequest{
				method: http.MethodPost, path: "/systemone", body: []byte(`{}`),
			})

			require.NoError(t, err)
			require.NotNil(t, resp)
			assert.Equal(t, tc.status, resp.statusCode)
			assert.Equal(t, `{"error":{"message":"boom"}}`, string(resp.body))

			bodies := rt.recorded()
			require.Len(t, bodies, tc.wantAttempts)
			for i, body := range bodies {
				assert.Equal(t, int32(1), body.closes.Load(),
					"response body of attempt %d must be closed exactly once", i+1)
			}
		})
	}
}

func TestTransportReadsWholeBodyAndHeaders(t *testing.T) {
	payload := `{"model":"typesafe/jev-1.13","answers":{"a":{"type":"noul","noul":0.73}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "gen-dec-1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	tr := newTransport(srv.URL, srv.Client(), time.Second, retryTestPolicy(0, time.Millisecond, 0), nil)
	resp, err := tr.do(context.Background(), &httpRequest{
		method: http.MethodPost, path: "/systemone", body: []byte(`{}`),
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusOK, resp.statusCode)
	assert.Equal(t, payload, string(resp.body))
	assert.Equal(t, "application/json", resp.header.Get("Content-Type"))
	assert.Equal(t, "gen-dec-1", resp.header.Get("X-Request-Id"))
}

// ---------------------------------------------------------------------------
// logging
// ---------------------------------------------------------------------------

func TestTransportLogsNeverContainCredentials(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	const secret = "sk-do-not-log-me-abc123"
	capture := &requestCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture.record(r) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	tr := newTransport(srv.URL, srv.Client(), time.Second,
		retryTestPolicy(2, time.Millisecond, time.Millisecond), logger)

	_, err := tr.do(context.Background(), &httpRequest{
		method: http.MethodPost,
		path:   "/systemone",
		body:   []byte(`{"model":"m"}`),
		header: http.Header{"Authorization": {"Bearer " + secret}},
	})
	require.NoError(t, err)

	out := buf.String()
	require.NotEmpty(t, out, "a debug logger attached to the transport must produce output")
	assert.NotContains(t, out, secret, "the Authorization value must never be logged")
	assert.NotContains(t, out, "Authorization", "header values are not logged at all")
	// The request body is logged on purpose, mirroring the official SDK; slog's
	// text handler escapes the quotes around the JSON keys.
	assert.Contains(t, out, `{\"model\":\"m\"}`)
	assert.Contains(t, out, "sending request")
	assert.Contains(t, out, "retrying after HTTP status")
}

// ---------------------------------------------------------------------------
// response size bound
// ---------------------------------------------------------------------------

func TestTransportMaxResponseBytesConstantAndDefaultLimit(t *testing.T) {
	assert.Equal(t, 32<<20, MaxResponseBytes, "the exported bound is 32 MiB")
	assert.Positive(t, MaxResponseBytes)

	tr := newTransport("http://example.invalid", nil, time.Second, retryTestPolicy(0, time.Millisecond, 0), nil)
	assert.Equal(t, int64(MaxResponseBytes), tr.responseLimit(),
		"a transport without a test override must use the exported constant")
}

func TestTransportResponseSizeLimit(t *testing.T) {
	const limit = 64

	tests := []struct {
		name    string
		size    int
		wantErr bool
	}{
		{"body well below the limit", limit / 2, false},
		{"body exactly at the limit", limit, false},
		{"body one byte over the limit", limit + 1, true},
		{"body far over the limit", 10 * limit, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rt := &bodyRecordingRoundTripper{
				status:  http.StatusOK,
				payload: bytes.Repeat([]byte("a"), tc.size),
			}
			policy := retryTestPolicy(2, time.Millisecond, time.Millisecond)
			policy.RetryConnectionErrors = true // an oversized body must still not be retried
			tr := newTransport("http://example.invalid", &http.Client{Transport: rt}, time.Second, policy, nil)
			tr.maxResponseBytes = limit // keep the test at 64 bytes instead of 32 MiB

			resp, err := tr.do(context.Background(), &httpRequest{
				method: http.MethodPost, path: "/systemone", body: []byte(`{}`),
			})

			bodies := rt.recorded()
			require.Len(t, bodies, 1, "an oversized body must not be retried")
			assert.Equal(t, int32(1), bodies[0].closes.Load(), "the body must be closed even when it is rejected")

			if !tc.wantErr {
				require.NoError(t, err)
				require.NotNil(t, resp)
				assert.Len(t, resp.body, tc.size)
				return
			}

			assert.Nil(t, resp)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrDecode, "an unreadable-in-full body is a decode failure")
			assert.NotErrorIs(t, err, ErrConnection, "an oversized body is not a connection failure")
			assert.Contains(t, err.Error(), "MaxResponseBytes")
		})
	}
}

// TestTransportRejectsRealOversizeBody exercises the exported 32 MiB bound for
// real. It is skipped in short mode because it moves 32 MiB.
func TestTransportRejectsRealOversizeBody(t *testing.T) {
	if testing.Short() {
		t.Skip("buffers MaxResponseBytes: skipped in -short mode")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, io.LimitReader(neverEndingReader{}, MaxResponseBytes+1))
	}))
	defer srv.Close()

	tr := newTransport(srv.URL, srv.Client(), 60*time.Second, retryTestPolicy(0, time.Millisecond, 0), nil)
	resp, err := tr.do(context.Background(), &httpRequest{
		method: http.MethodPost, path: "/systemone", body: []byte(`{}`),
	})

	assert.Nil(t, resp)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDecode)
}

// neverEndingReader yields 'a' bytes forever, without allocating a buffer.
type neverEndingReader struct{}

func (neverEndingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}

// ---------------------------------------------------------------------------
// redirects
// ---------------------------------------------------------------------------

func TestTransportReturnsRedirectsUntouched(t *testing.T) {
	tests := []struct {
		name   string
		policy RetryPolicy
	}{
		{"retry policy without 3xx", retryTestPolicy(2, time.Millisecond, time.Millisecond)},
		{"default retry policy", DefaultRetryPolicy()},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			capture := &requestCapture{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capture.record(r)
				w.Header().Set("Location", "/moved")
				w.WriteHeader(http.StatusFound)
				_, _ = w.Write([]byte(`{"error":{"message":"moved","code":302}}`))
			}))
			defer srv.Close()

			// An http.Client that does not follow redirects, as the client builds.
			hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}}
			tr := newTransport(srv.URL, hc, time.Second, tc.policy, nil)

			resp, err := tr.do(context.Background(), &httpRequest{
				method: http.MethodPost, path: "/systemone", body: []byte(`{}`),
			})

			require.NoError(t, err, "3xx is not a transport error")
			require.NotNil(t, resp)
			assert.Equal(t, http.StatusFound, resp.statusCode)
			assert.Equal(t, "/moved", resp.header.Get("Location"))
			assert.Equal(t, `{"error":{"message":"moved","code":302}}`, string(resp.body))
			assert.Equal(t, 1, capture.count(), "a redirect must not be retried")
		})
	}
}
