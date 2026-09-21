package systemone

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noRetry keeps tests fast: retry behaviour is exercised in retry_test.go and
// transport_test.go, not here.
func noRetry() Option { return WithRetryPolicy(RetryPolicy{MaxRetries: 0}) }

// okBody is a minimal successful response with every gateway-optional field
// absent, so that a client pointed at the official host is covered too.
const okBody = `{
  "model": "jev-1.13.0",
  "answers": {"greeting": {"type": "noul", "noul": 0.91}},
  "usage": {"input_tokens": 120, "output_tokens": 12}
}`

func greeting() Questions {
	return Questions{"greeting": NoulQuestion{Instructions: "Is this a greeting?"}}
}

// newTestClient wires a client to a test server with retries disabled.
func newTestClient(t *testing.T, handler http.Handler, extra ...Option) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	opts := append([]Option{WithBaseURL(srv.URL), WithAPIKey("test-key"), noRetry()}, extra...)
	c, err := New(opts...)
	require.NoError(t, err)
	return c
}

// ---------- construction ----------

func TestNewRequiresAPIKey(t *testing.T) {
	t.Setenv(EnvAPIKey, "")
	_, err := New(WithBaseURL("https://example.test/v1"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrValidation)
	assert.Contains(t, err.Error(), EnvAPIKey)
}

func TestNewExplicitAPIKeyBeatsEnvironment(t *testing.T) {
	t.Setenv(EnvAPIKey, "from-env")

	var gotAuth string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, okBody)
	}), WithAPIKey("explicit"))

	_, err := c.Evaluate(context.Background(), "hi", greeting())
	require.NoError(t, err)
	assert.Equal(t, "Bearer explicit", gotAuth)
}

func TestNewReadsEnvironment(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = io.WriteString(w, okBody)
	}))
	defer srv.Close()

	t.Setenv(EnvAPIKey, "env-key")
	t.Setenv(EnvBaseURL, srv.URL)
	t.Setenv(EnvModel, "env-model")

	c, err := New(noRetry())
	require.NoError(t, err)
	assert.Equal(t, "env-model", c.DefaultModel())

	_, err = c.Evaluate(context.Background(), "hi", greeting())
	require.NoError(t, err)

	assert.Equal(t, "Bearer env-key", gotAuth)
	assert.Equal(t, "/systemone", gotPath)
	assert.Equal(t, "env-model", gotBody["model"])
}

func TestNewRejectsRelativeBaseURL(t *testing.T) {
	_, err := New(WithAPIKey("k"), WithBaseURL("api.example.com/v1"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrValidation)
}

func TestNewRejectsInvalidRetryPolicy(t *testing.T) {
	cases := map[string]RetryPolicy{
		"negative retries":  {MaxRetries: -1},
		"jitter above one":  {MaxRetries: 1, BackoffJitter: 1.5},
		"jitter below zero": {MaxRetries: 1, BackoffJitter: -0.1},
		"initial over max":  {MaxRetries: 1, InitialBackoff: time.Second, MaxBackoff: time.Millisecond},
		"negative backoff":  {MaxRetries: 1, InitialBackoff: -time.Second},
	}
	for name, policy := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := New(WithAPIKey("k"), WithRetryPolicy(policy))
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrValidation)
		})
	}
}

func TestNewTrimsTrailingSlashFromBaseURL(t *testing.T) {
	c, err := New(WithAPIKey("k"), WithBaseURL("https://example.test/v1/"))
	require.NoError(t, err)
	assert.Equal(t, "https://example.test/v1", c.BaseURL())
}

// ---------- request shape ----------

func TestEvaluateSendsExpectedRequest(t *testing.T) {
	// This test asserts that an unset model falls back to DefaultModel, so an
	// exported TYPESAFE_DEFAULT_MODEL would otherwise be the thing under test.
	clearClientEnv(t)

	var gotMethod, gotPath, gotCT, gotAccept, gotUA string
	var gotBody map[string]any

	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotCT, gotAccept, gotUA = r.Header.Get("Content-Type"), r.Header.Get("Accept"), r.Header.Get("User-Agent")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		_, _ = io.WriteString(w, okBody)
	}))

	_, err := c.Evaluate(context.Background(), map[string]any{"message": "hello"}, greeting())
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/systemone", gotPath)
	assert.Equal(t, "application/json", gotCT)
	assert.Equal(t, "application/json", gotAccept)
	assert.Equal(t, UserAgent(), gotUA)

	assert.Equal(t, DefaultModel, gotBody["model"])
	assert.Equal(t, map[string]any{"message": "hello"}, gotBody["state"])

	questions, ok := gotBody["questions"].(map[string]any)
	require.True(t, ok, "questions must be an object")
	assert.Equal(t, "noul", questions["greeting"].(map[string]any)["type"])
}

func TestEvaluateSendsStateAsStringWhenGivenString(t *testing.T) {
	var gotState any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		gotState = body["state"]
		_, _ = io.WriteString(w, okBody)
	}))

	_, err := c.Evaluate(context.Background(), "plain text", greeting())
	require.NoError(t, err)
	assert.Equal(t, "plain text", gotState)
}

// TestEvaluateDoesNotHTMLEscapeRequestBody guards a Go-specific default.
//
// encoding/json escapes <, > and & unless told otherwise; the reference
// JavaScript client does not. Both spellings are valid JSON and decode to the
// same value, so nothing else in this package would ever notice the difference.
func TestEvaluateDoesNotHTMLEscapeRequestBody(t *testing.T) {
	var raw []byte
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		raw, err = io.ReadAll(r.Body)
		require.NoError(t, err)
		_, _ = io.WriteString(w, okBody)
	}))

	const state = `<script>alert("a & b")</script>`
	_, err := c.Evaluate(context.Background(), state, greeting())
	require.NoError(t, err)

	body := string(raw)
	assert.NotContains(t, body, `\u003c`, `'<' must not be escaped to \u003c`)
	assert.NotContains(t, body, `\u003e`, `'>' must not be escaped to \u003e`)
	assert.NotContains(t, body, `\u0026`, `'&' must not be escaped to \u0026`)
	assert.Contains(t, body, "<script>")

	var decoded struct {
		State string `json:"state"`
	}
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Equal(t, state, decoded.State, "round trip must preserve the original text")
}

func TestEvaluateCallModelOverridesDefault(t *testing.T) {
	var gotModel string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		gotModel, _ = body["model"].(string)
		_, _ = io.WriteString(w, okBody)
	}), WithModel("default-model"))

	_, err := c.Evaluate(context.Background(), "hi", greeting(), WithCallModel("call-model"))
	require.NoError(t, err)
	assert.Equal(t, "call-model", gotModel)
}

func TestCallerCannotClobberAuthoritativeHeaders(t *testing.T) {
	var gotAuth, gotCT string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotCT = r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		_, _ = io.WriteString(w, okBody)
	}))

	hostile := http.Header{}
	hostile.Set("Authorization", "Bearer attacker")
	hostile.Set("Content-Type", "text/plain")

	_, err := c.Evaluate(context.Background(), "hi", greeting(), WithCallHeaders(hostile))
	require.NoError(t, err)
	assert.Equal(t, "Bearer test-key", gotAuth)
	assert.Equal(t, "application/json", gotCT)
}

func TestWithHeaderAddsCustomHeaders(t *testing.T) {
	var gotRequestID, gotTenant string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequestID, gotTenant = r.Header.Get("X-Request-Id"), r.Header.Get("X-Tenant")
		_, _ = io.WriteString(w, okBody)
	}),
		WithHeader("X-Request-ID", "req-1"),
		WithHeader("X-Tenant", "acme"),
	)

	_, err := c.Evaluate(context.Background(), "hi", greeting())
	require.NoError(t, err)
	assert.Equal(t, "req-1", gotRequestID)
	assert.Equal(t, "acme", gotTenant)
}

// ---------- responses ----------

func TestEvaluateDecodesResult(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, okBody)
	}))

	res, err := c.Evaluate(context.Background(), "hi", greeting())
	require.NoError(t, err)

	assert.Equal(t, "jev-1.13.0", res.Model)
	assert.Empty(t, res.ID, "gateway-only field stays empty")
	assert.Empty(t, res.Provider, "gateway-only field stays empty")
	assert.Equal(t, 120, res.Usage.InputTokens)
	assert.Equal(t, 12, res.Usage.OutputTokens)
	assert.Nil(t, res.Usage.Cost, "gateway-only field stays nil")

	noul, ok := res.Noul("greeting")
	require.True(t, ok)
	assert.InDelta(t, 0.91, noul.Noul, 1e-9)
}

func TestEvaluateDecodesGatewayExtraFields(t *testing.T) {
	body := `{
	  "model": "typesafe/jev-1.13-20260917",
	  "provider": "TypeSafe",
	  "id": "gen-dec-1",
	  "answers": {"greeting": {"type": "noul", "noul": 0.5}},
	  "usage": {"input_tokens": 1, "output_tokens": 2, "cost": 1.7e-05}
	}`
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	}))

	res, err := c.Evaluate(context.Background(), "hi", greeting())
	require.NoError(t, err)
	assert.Equal(t, "TypeSafe", res.Provider)
	assert.Equal(t, "gen-dec-1", res.ID)
	require.NotNil(t, res.Usage.Cost)
	assert.InDelta(t, 1.7e-05, *res.Usage.Cost, 1e-12)
}

func TestEvaluateRejectsMalformedSuccessBody(t *testing.T) {
	cases := map[string]string{
		"empty":         ``,
		"truncated":     `{"answers":`,
		"not an object": `[]`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			_, err := c.Evaluate(context.Background(), "hi", greeting())
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrDecode)
		})
	}
}

// ---------- error mapping ----------

func TestEvaluateMapsHTTPErrors(t *testing.T) {
	cases := []struct {
		status   int
		body     string
		sentinel error
	}{
		{http.StatusBadRequest, `{"error":{"message":"Model nope does not exist","code":400}}`, ErrAPI},
		{http.StatusUnauthorized, `{"error":{"message":"No cookie auth credentials found","code":401}}`, ErrAPI},
		{http.StatusInternalServerError, `{"error":{"message":"boom","code":500}}`, ErrAPI},
		{http.StatusTooManyRequests, `{"error":{"message":"slow down","code":429}}`, ErrRateLimit},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))

			_, err := c.Evaluate(context.Background(), "hi", greeting())
			require.Error(t, err)
			assert.ErrorIs(t, err, tc.sentinel)
			assert.ErrorIs(t, err, ErrAPI, "every HTTP error is also an API error")

			var apiErr *APIError
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, tc.status, apiErr.StatusCode)
		})
	}
}

func TestEvaluateRateLimitCarriesRetryAfter(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"slow down","code":429}}`)
	}))

	_, err := c.Evaluate(context.Background(), "hi", greeting())
	require.Error(t, err)

	var rateErr *RateLimitError
	require.ErrorAs(t, err, &rateErr)
	assert.Equal(t, 2*time.Second, rateErr.RetryAfter)
	assert.Equal(t, http.StatusTooManyRequests, rateErr.StatusCode)
}

func TestEvaluateNonJSONErrorBodyIsPreserved(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "<html>gateway blew up</html>")
	}))

	_, err := c.Evaluate(context.Background(), "hi", greeting())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAPI)

	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Contains(t, apiErr.Message, "gateway blew up")
	assert.Equal(t, "<html>gateway blew up</html>", string(apiErr.Body))
}

// ---------- validation happens before the network ----------

func TestEvaluateRejectsInvalidQuestionsWithoutCallingTheServer(t *testing.T) {
	var called atomic.Bool
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called.Store(true)
		_, _ = io.WriteString(w, okBody)
	}))

	_, err := c.Evaluate(context.Background(), "hi", Questions{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrValidation)
	assert.False(t, called.Load(), "no request may be sent for an invalid question set")
}

// ---------- cancellation and deadlines ----------

func TestEvaluateHonoursCallerCancellation(t *testing.T) {
	release := make(chan struct{})
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		_, _ = io.WriteString(w, okBody)
	}))
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := c.Evaluate(ctx, "hi", greeting())
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAborted)
	assert.Less(t, elapsed, 2*time.Second, "cancellation must not wait for the default timeout")
}

func TestEvaluateHonoursTotalTimeout(t *testing.T) {
	release := make(chan struct{})
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		_, _ = io.WriteString(w, okBody)
	}), WithTotalTimeout(50*time.Millisecond))
	defer close(release)

	start := time.Now()
	_, err := c.Evaluate(context.Background(), "hi", greeting())
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTimeout)
	assert.Less(t, elapsed, 2*time.Second)
}

func TestEvaluateDisablesDefaultRetriesWhenZero(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"boom","code":500}}`)
	}))

	_, err := c.Evaluate(context.Background(), "hi", greeting())
	require.Error(t, err)
	assert.Equal(t, int32(1), calls.Load())
}

// fastRetryPolicy retries n times with millisecond backoff, so retry behaviour
// can be asserted without every test paying the default 500ms schedule.
func fastRetryPolicy(n int) RetryPolicy {
	return RetryPolicy{
		MaxRetries:            n,
		InitialBackoff:        time.Millisecond,
		MaxBackoff:            2 * time.Millisecond,
		BackoffJitter:         0,
		RetryStatuses:         []int{408, 429, 500, 502, 503},
		RespectRetryAfter:     true,
		MaxRetryAfter:         time.Minute,
		RetryConnectionErrors: true,
		RetryTimeoutErrors:    true,
	}
}

// TestCallRetryPolicyOverrideTakesEffect pins a defect that shipped once.
//
// WithCallRetryPolicy computed a policy and validated it, then dropped it on the
// floor: the transport kept the policy it was built with, so the documented
// per-call override silently did nothing. Nothing caught it because no test
// asserted the override's effect — the validation error path made the option
// look exercised. staticcheck's SA4006 (value never used) is what surfaced it.
func TestCallRetryPolicyOverrideTakesEffect(t *testing.T) {
	failing := func(calls *atomic.Int32) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"message":"boom","code":500}}`)
		})
	}

	t.Run("the call policy retries more than the client policy", func(t *testing.T) {
		var calls atomic.Int32
		c := newTestClient(t, failing(&calls)) // client policy: no retries

		_, err := c.Evaluate(context.Background(), "hi", greeting(),
			WithCallRetryPolicy(fastRetryPolicy(2)))
		require.Error(t, err)
		assert.Equal(t, int32(3), calls.Load(), "the per-call policy must win over the client's")
	})

	t.Run("the call policy can also retry less", func(t *testing.T) {
		var calls atomic.Int32
		c := newTestClient(t, failing(&calls), WithRetryPolicy(fastRetryPolicy(4)))

		_, err := c.Evaluate(context.Background(), "hi", greeting(),
			WithCallRetryPolicy(fastRetryPolicy(1)))
		require.Error(t, err)
		assert.Equal(t, int32(2), calls.Load())
	})

	t.Run("an invalid call policy is rejected before any request", func(t *testing.T) {
		var calls atomic.Int32
		c := newTestClient(t, failing(&calls))

		_, err := c.Evaluate(context.Background(), "hi", greeting(),
			WithCallRetryPolicy(RetryPolicy{MaxRetries: -1}))
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrValidation)
		assert.Equal(t, int32(0), calls.Load())
	})

	t.Run("a call policy does not leak into concurrent calls", func(t *testing.T) {
		var calls atomic.Int32
		c := newTestClient(t, failing(&calls))

		done := make(chan error, 2)
		go func() {
			_, err := c.Evaluate(context.Background(), "hi", greeting(),
				WithCallRetryPolicy(fastRetryPolicy(2)))
			done <- err
		}()
		go func() {
			_, err := c.Evaluate(context.Background(), "hi", greeting())
			done <- err
		}()
		require.Error(t, <-done)
		require.Error(t, <-done)

		// One call retried twice, the other did not: 3 + 1.
		assert.Equal(t, int32(4), calls.Load())
	})
}

// TestEvaluateRefusesToFollowRedirects pins the decision not to follow 3xx.
//
// Go's default policy rewrites a 301/302/303 POST into a GET, drops the body,
// and replays the Authorization header at whatever location the server named.
// For an endpoint whose contract is a single POST to a fixed URL, that is a
// server-controlled rewrite of the request, so the 3xx surfaces as an error.
func TestEvaluateRefusesToFollowRedirects(t *testing.T) {
	var followed atomic.Bool
	var gotAuth []string

	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		followed.Store(true)
		gotAuth = r.Header.Values("Authorization")
		_, _ = io.WriteString(w, okBody)
	}))
	defer dest.Close()

	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL, http.StatusFound)
	}))

	_, err := c.Evaluate(context.Background(), "hi", greeting())
	require.Error(t, err)

	assert.False(t, followed.Load(), "the redirect target must never be contacted")
	assert.Empty(t, gotAuth, "credentials must never reach the redirect target")

	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusFound, apiErr.StatusCode)
}

// ---------- misc ----------

func TestWithLoggerDoesNotLeakCredentials(t *testing.T) {
	var buf strings.Builder
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, okBody)
	}), WithLogger(logger))

	_, err := c.Evaluate(context.Background(), "sensitive user prompt", greeting())
	require.NoError(t, err)

	logged := buf.String()
	assert.NotEmpty(t, logged, "debug logging should produce output")
	assert.NotContains(t, logged, "test-key", "the API key must never be logged")
	assert.NotContains(t, logged, "Bearer", "the Authorization header must never be logged")
}

func TestClientCanBeReusedConcurrently(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, okBody)
	}))

	const n = 32
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := c.Evaluate(context.Background(), "hi", greeting())
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		require.NoError(t, <-errs)
	}
}

func TestEvaluateReportsWhetherANameExists(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, okBody)
	}))

	res, err := c.Evaluate(context.Background(), "hi", greeting())
	require.NoError(t, err)

	_, ok := res.Noul("missing")
	assert.False(t, ok)

	_, ok = res.Choice("greeting")
	assert.False(t, ok, "a noul answer must not be readable as a choice")

	_, ok = res.Score("greeting")
	assert.False(t, ok, "a noul answer must not be readable as a score")
}
