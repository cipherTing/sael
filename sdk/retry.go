package sdk

import (
	"net/http"
	"time"
)

// RetryPolicy describes when and how the transport retries a request.
//
// The zero value is valid and means "never retry": MaxRetries is 0, so a single
// attempt is made (RetryStatuses being empty also disables status-based
// retries, and both error classes are off).
//
// The defaults mirror the official TypeScript SDK exactly; see
// DefaultRetryPolicy.
type RetryPolicy struct {
	// MaxRetries is the number of retries *after* the first attempt. A value of
	// 2 means the request is attempted at most 3 times. Negative values are
	// treated as 0.
	MaxRetries int

	// InitialBackoff is the delay before the second attempt.
	InitialBackoff time.Duration

	// MaxBackoff caps the exponential backoff interval. A non-positive value
	// means "uncapped".
	MaxBackoff time.Duration

	// BackoffJitter is the fraction (0..1) randomly subtracted from each
	// computed backoff interval, so the actual wait is
	// interval*(1 - jitter*rand[0,1)). Values outside 0..1 are clamped.
	BackoffJitter float64

	// RetryStatuses lists the HTTP status codes that should be retried.
	RetryStatuses []int

	// RespectRetryAfter honours a Retry-After response header instead of the
	// computed backoff.
	RespectRetryAfter bool

	// MaxRetryAfter is the largest Retry-After the transport is willing to
	// honour. A non-positive value means "no limit": any Retry-After is used as
	// given.
	MaxRetryAfter time.Duration

	// RetryConnectionErrors retries transport-level failures (dial failures,
	// connection resets, truncated bodies, ...).
	RetryConnectionErrors bool

	// RetryTimeoutErrors retries attempts that ran into the per-attempt timeout.
	RetryTimeoutErrors bool
}

// DefaultRetryPolicy returns the retry policy of the official TypeScript SDK:
// 2 retries after the first attempt, on 408, 429 and 5xx, backing off from
// 500ms doubling to 5s with up to 25% jitter subtracted, honouring Retry-After
// up to 60s.
//
// Every call returns a fresh RetryStatuses slice, so callers may mutate the
// result without affecting other callers.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxRetries:            2,
		InitialBackoff:        500 * time.Millisecond,
		MaxBackoff:            5 * time.Second,
		BackoffJitter:         0.25,
		RetryStatuses:         defaultRetryStatuses(),
		RespectRetryAfter:     true,
		MaxRetryAfter:         60 * time.Second,
		RetryConnectionErrors: true,
		RetryTimeoutErrors:    true,
	}
}

// defaultRetryStatuses returns 408, 429 and the whole 500..599 range.
func defaultRetryStatuses() []int {
	statuses := make([]int, 0, 2+100)
	statuses = append(statuses, http.StatusRequestTimeout, http.StatusTooManyRequests)
	for status := 500; status <= 599; status++ {
		statuses = append(statuses, status)
	}
	return statuses
}

// maxAttempts is the total number of attempts, first one included.
func (p RetryPolicy) maxAttempts() int {
	if p.MaxRetries < 0 {
		return 1
	}
	return p.MaxRetries + 1
}

// retryableStatus reports whether an HTTP status code should be retried.
func (p RetryPolicy) retryableStatus(code int) bool {
	for _, s := range p.RetryStatuses {
		if s == code {
			return true
		}
	}
	return false
}
