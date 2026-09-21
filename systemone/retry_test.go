package systemone

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultRetryPolicyFields(t *testing.T) {
	p := DefaultRetryPolicy()

	assert.Equal(t, 2, p.MaxRetries, "two retries after the first attempt")
	assert.Equal(t, 500*time.Millisecond, p.InitialBackoff)
	assert.Equal(t, 5*time.Second, p.MaxBackoff)
	assert.Equal(t, 0.25, p.BackoffJitter)
	assert.True(t, p.RespectRetryAfter)
	assert.Equal(t, 60*time.Second, p.MaxRetryAfter)
	assert.True(t, p.RetryConnectionErrors)
	assert.True(t, p.RetryTimeoutErrors)

	want := []int{http.StatusRequestTimeout, http.StatusTooManyRequests}
	for status := 500; status <= 599; status++ {
		want = append(want, status)
	}
	assert.Equal(t, want, p.RetryStatuses, "408, 429 and the whole 500..599 range, and nothing else")
}

func TestDefaultRetryPolicyReturnsIndependentSlices(t *testing.T) {
	first := DefaultRetryPolicy()
	require.NotEmpty(t, first.RetryStatuses)
	first.RetryStatuses[0] = 999

	second := DefaultRetryPolicy()
	assert.Equal(t, http.StatusRequestTimeout, second.RetryStatuses[0],
		"mutating one policy must not affect the next DefaultRetryPolicy call")
}

func TestRetryPolicyRetryableStatus(t *testing.T) {
	tests := []struct {
		name   string
		policy RetryPolicy
		status int
		want   bool
	}{
		{"default retries 408", DefaultRetryPolicy(), 408, true},
		{"default retries 429", DefaultRetryPolicy(), 429, true},
		{"default retries 500", DefaultRetryPolicy(), 500, true},
		{"default retries 550", DefaultRetryPolicy(), 550, true},
		{"default retries 599", DefaultRetryPolicy(), 599, true},
		{"default does not retry 200", DefaultRetryPolicy(), 200, false},
		{"default does not retry 400", DefaultRetryPolicy(), 400, false},
		{"default does not retry 401", DefaultRetryPolicy(), 401, false},
		{"default does not retry 404", DefaultRetryPolicy(), 404, false},
		{"default does not retry 422", DefaultRetryPolicy(), 422, false},
		{"default does not retry 600", DefaultRetryPolicy(), 600, false},
		{"zero policy retries nothing", RetryPolicy{}, 500, false},
		{"custom list is authoritative", RetryPolicy{RetryStatuses: []int{418}}, 418, true},
		{"custom list excludes 500", RetryPolicy{RetryStatuses: []int{418}}, 500, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.policy.retryableStatus(tc.status))
		})
	}
}

func TestRetryPolicyMaxAttempts(t *testing.T) {
	tests := []struct {
		maxRetries int
		want       int
	}{
		{0, 1},
		{1, 2},
		{2, 3},
		{10, 11},
		{-1, 1},
		{-5, 1},
	}

	for _, tc := range tests {
		assert.Equal(t, tc.want, RetryPolicy{MaxRetries: tc.maxRetries}.maxAttempts(),
			"MaxRetries=%d", tc.maxRetries)
	}
}

// newDelayTransport builds a transport only used to exercise retryDelay; the
// base URL is never dialled.
func newDelayTransport(p RetryPolicy) *transport {
	return newTransport("http://example.invalid", nil, time.Second, p, nil)
}

func TestTransportRetryDelayHonoursRetryAfter(t *testing.T) {
	tests := []struct {
		name      string
		respect   bool
		maxRetry  time.Duration
		header    http.Header
		wantDelay time.Duration
		exact     bool
	}{
		{
			name: "seconds form is used verbatim", respect: true, maxRetry: 60 * time.Second,
			header: http.Header{"Retry-After": {"1"}}, wantDelay: time.Second, exact: true,
		},
		{
			name: "seconds form below the cap is used", respect: true, maxRetry: 5 * time.Second,
			header: http.Header{"Retry-After": {"3"}}, wantDelay: 3 * time.Second, exact: true,
		},
		{
			name: "zero seconds means retry now", respect: true, maxRetry: 60 * time.Second,
			header: http.Header{"Retry-After": {"0"}}, wantDelay: 0, exact: true,
		},
		{
			name: "above the cap falls back to backoff", respect: true, maxRetry: 5 * time.Second,
			header: http.Header{"Retry-After": {"120"}}, wantDelay: 20 * time.Millisecond, exact: true,
		},
		{
			name: "not respected falls back to backoff", respect: false, maxRetry: 60 * time.Second,
			header: http.Header{"Retry-After": {"120"}}, wantDelay: 20 * time.Millisecond, exact: true,
		},
		{
			name: "absent header falls back to backoff", respect: true, maxRetry: 60 * time.Second,
			header: http.Header{}, wantDelay: 20 * time.Millisecond, exact: true,
		},
		{
			name: "garbage falls back to backoff", respect: true, maxRetry: 60 * time.Second,
			header: http.Header{"Retry-After": {"soon"}}, wantDelay: 20 * time.Millisecond, exact: true,
		},
		{
			name: "past HTTP-date means retry now", respect: true, maxRetry: 60 * time.Second,
			header:    http.Header{"Retry-After": {time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)}},
			wantDelay: 0, exact: true,
		},
		{
			name: "future HTTP-date is relative to now", respect: true, maxRetry: 60 * time.Second,
			header:    http.Header{"Retry-After": {time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)}},
			wantDelay: 30 * time.Second, exact: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := retryTestPolicy(3, 20*time.Millisecond, time.Second)
			policy.RespectRetryAfter = tc.respect
			policy.MaxRetryAfter = tc.maxRetry
			tr := newDelayTransport(policy)

			got := tr.retryDelay(tr.policy, tr.newBackoffSequence(tr.policy), tc.header)

			if tc.exact {
				assert.Equal(t, tc.wantDelay, got)
				return
			}
			// The date form resolves against the wall clock, so allow a little
			// slack while still proving the date was parsed and honoured.
			assert.GreaterOrEqual(t, got, 25*time.Second)
			assert.LessOrEqual(t, got, tc.wantDelay)
		})
	}
}

func TestTransportRetryDelayBackoffSequence(t *testing.T) {
	tests := []struct {
		name    string
		initial time.Duration
		max     time.Duration
		want    []time.Duration
	}{
		{
			name:    "official defaults double and cap at 5s",
			initial: 500 * time.Millisecond, max: 5 * time.Second,
			want: []time.Duration{
				500 * time.Millisecond,
				1 * time.Second,
				2 * time.Second,
				4 * time.Second,
				5 * time.Second,
				5 * time.Second,
			},
		},
		{
			name:    "doubling stops at the cap",
			initial: 10 * time.Millisecond, max: 25 * time.Millisecond,
			want: []time.Duration{
				10 * time.Millisecond,
				20 * time.Millisecond,
				25 * time.Millisecond,
				25 * time.Millisecond,
			},
		},
		{
			name:    "non-positive max means uncapped",
			initial: 10 * time.Millisecond, max: 0,
			want: []time.Duration{
				10 * time.Millisecond,
				20 * time.Millisecond,
				40 * time.Millisecond,
				80 * time.Millisecond,
			},
		},
		{
			name:    "zero initial backoff stays zero",
			initial: 0, max: time.Second,
			want: []time.Duration{0, 0, 0},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := retryTestPolicy(len(tc.want), tc.initial, tc.max)
			policy.RespectRetryAfter = false
			tr := newDelayTransport(policy)

			sequence := tr.newBackoffSequence(tr.policy)
			got := make([]time.Duration, 0, len(tc.want))
			for range tc.want {
				got = append(got, tr.retryDelay(tr.policy, sequence, nil))
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestTransportRetryDelayJitterIsSubtractedWithinBounds(t *testing.T) {
	const base = 100 * time.Millisecond

	policy := retryTestPolicy(3, base, time.Second)
	policy.BackoffJitter = 0.25
	policy.RespectRetryAfter = false
	tr := newDelayTransport(policy)

	min := time.Duration(float64(base) * 0.75)
	seen := make(map[time.Duration]struct{}, 8)

	for i := 0; i < 200; i++ {
		got := tr.retryDelay(tr.policy, tr.newBackoffSequence(tr.policy), nil)
		assert.GreaterOrEqual(t, got, min, "jitter must never subtract more than BackoffJitter")
		assert.LessOrEqual(t, got, base, "jitter must never add to the interval")
		seen[got] = struct{}{}
	}

	assert.Greater(t, len(seen), 1, "jitter should vary between attempts")
}

func TestTransportRetryDelayJitterIsClamped(t *testing.T) {
	tests := []struct {
		name   string
		jitter float64
		min    time.Duration
		max    time.Duration
	}{
		{name: "jitter above one is clamped to one", jitter: 1.5, min: 0, max: 100 * time.Millisecond},
		{name: "jitter of one can subtract everything", jitter: 1, min: 0, max: 100 * time.Millisecond},
		{name: "negative jitter is ignored", jitter: -1, min: 100 * time.Millisecond, max: 100 * time.Millisecond},
		{name: "zero jitter is exact", jitter: 0, min: 100 * time.Millisecond, max: 100 * time.Millisecond},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := retryTestPolicy(3, 100*time.Millisecond, time.Second)
			policy.BackoffJitter = tc.jitter
			policy.RespectRetryAfter = false
			tr := newDelayTransport(policy)

			for i := 0; i < 50; i++ {
				got := tr.retryDelay(tr.policy, tr.newBackoffSequence(tr.policy), nil)
				assert.GreaterOrEqual(t, got, tc.min)
				assert.LessOrEqual(t, got, tc.max)
			}
		})
	}
}

func TestTransportRetryDelayFallsBackWhenRetryAfterExceedsCap(t *testing.T) {
	policy := retryTestPolicy(3, 30*time.Millisecond, time.Second)
	policy.RespectRetryAfter = true
	policy.MaxRetryAfter = 5 * time.Second
	tr := newDelayTransport(policy)

	// 120s is beyond the cap, so the exponential sequence takes over.
	got := tr.retryDelay(tr.policy, tr.newBackoffSequence(tr.policy), http.Header{"Retry-After": {"120"}})
	assert.Equal(t, 30*time.Millisecond, got)

	// A header inside the cap still wins.
	got = tr.retryDelay(tr.policy, tr.newBackoffSequence(tr.policy), http.Header{"Retry-After": {"2"}})
	assert.Equal(t, 2*time.Second, got)
}

func TestTransportRetryDelayNonPositiveMaxRetryAfterMeansNoLimit(t *testing.T) {
	policy := retryTestPolicy(3, 30*time.Millisecond, time.Second)
	policy.RespectRetryAfter = true
	policy.MaxRetryAfter = 0
	tr := newDelayTransport(policy)

	got := tr.retryDelay(tr.policy, tr.newBackoffSequence(tr.policy), http.Header{"Retry-After": {"90"}})
	assert.Equal(t, 90*time.Second, got)
}

func TestRetryIsTimeoutError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"os timeout", &timeoutError{}, true},
		{"plain error", errString("boom"), false},
		{"connection refused", &net.OpError{Op: "dial", Net: "tcp", Err: errString("connection refused")}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isTimeoutError(tc.err))
		})
	}
}

// timeoutError implements net.Error with Timeout() == true.
type timeoutError struct{}

func (*timeoutError) Error() string   { return "i/o timeout" }
func (*timeoutError) Timeout() bool   { return true }
func (*timeoutError) Temporary() bool { return true }
