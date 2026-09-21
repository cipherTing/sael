package systemone

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Sentinel errors, matchable with errors.Is. Every error this package returns
// wraps exactly one of these (or is a *APIError, which matches ErrAPI).
var (
	// ErrAPI matches any non-2xx response from the service.
	ErrAPI = errors.New("systemone: api error")
	// ErrRateLimit matches HTTP 429, and additionally carries a RetryAfter.
	ErrRateLimit = errors.New("systemone: rate limited")
	// ErrConnection matches a failure to reach the service at all.
	ErrConnection = errors.New("systemone: connection failed")
	// ErrTimeout matches a request that exceeded an attempt or total deadline.
	ErrTimeout = errors.New("systemone: request timed out")
	// ErrAborted matches cancellation through the caller's context.
	ErrAborted = errors.New("systemone: request aborted by caller")
	// ErrDecode matches a 2xx response whose body could not be interpreted.
	ErrDecode = errors.New("systemone: malformed response")
	// ErrValidation matches a request rejected before it was sent.
	ErrValidation = errors.New("systemone: invalid request")
	// ErrConfig matches a failure reading or writing the configuration
	// directory, or a setting in it that does not parse. It is deliberately
	// distinct from ErrValidation: a caller that falls back on a rejected
	// question set must not also swallow a broken config.json.
	ErrConfig = errors.New("systemone: invalid configuration")
)

// APIError is a non-2xx response from the service.
//
// Body holds the raw response payload because that is often the only usable
// diagnostic: validation failures return HTTP 400 with Message set to a
// JSON-encoded array of issues rather than a human sentence.
type APIError struct {
	StatusCode int
	Message    string
	Code       json.RawMessage // the server's "code" field; number or string
	Body       []byte
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("systemone: api error: HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("systemone: api error: HTTP %d: %s", e.StatusCode, e.Message)
}

// Is reports whether target is ErrAPI.
func (e *APIError) Is(target error) bool { return target == ErrAPI }

// RateLimitError is an APIError for HTTP 429.
type RateLimitError struct {
	APIError
	// RetryAfter is the wait the server asked for, or 0 when it asked for none.
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("%s (retry after %s)", e.APIError.Error(), e.RetryAfter)
	}
	return e.APIError.Error()
}

// Is reports whether target is ErrRateLimit or ErrAPI.
func (e *RateLimitError) Is(target error) bool {
	return target == ErrRateLimit || e.APIError.Is(target)
}

// As lets errors.As reach the embedded *APIError.
//
// Embedding APIError by value does not make *RateLimitError assignable to
// *APIError, so without this a caller cannot handle "any HTTP error" uniformly
// and then special-case rate limiting.
func (e *RateLimitError) As(target any) bool {
	if p, ok := target.(**APIError); ok {
		*p = &e.APIError
		return true
	}
	return false
}

// ConnectionError wraps a transport-level failure to reach the service.
type ConnectionError struct {
	Err error
}

func (e *ConnectionError) Error() string { return "systemone: connection failed: " + e.Err.Error() }

// Unwrap exposes the underlying cause.
func (e *ConnectionError) Unwrap() error { return e.Err }

// Is reports whether target is ErrConnection.
func (e *ConnectionError) Is(target error) bool { return target == ErrConnection }

// TimeoutError wraps a request that ran past its deadline.
type TimeoutError struct {
	Err     error
	Timeout time.Duration
}

func (e *TimeoutError) Error() string {
	if e.Timeout > 0 {
		return fmt.Sprintf("systemone: request timed out after %s: %v", e.Timeout, e.Err)
	}
	return "systemone: request timed out: " + e.Err.Error()
}

// Unwrap exposes the underlying cause.
func (e *TimeoutError) Unwrap() error { return e.Err }

// Is reports whether target is ErrTimeout.
func (e *TimeoutError) Is(target error) bool { return target == ErrTimeout }

// AbortError wraps cancellation through the caller's context.
type AbortError struct {
	Err error
}

func (e *AbortError) Error() string { return "systemone: request aborted by caller: " + e.Err.Error() }

// Unwrap exposes the underlying cause.
func (e *AbortError) Unwrap() error { return e.Err }

// Is reports whether target is ErrAborted.
func (e *AbortError) Is(target error) bool { return target == ErrAborted }

// errorEnvelope is the service's error shape:
//
//	{"error":{"message":"...","code":400}}
//
// Some responses also carry a top-level "user_id", which is ignored here.
type errorEnvelope struct {
	Error struct {
		Message string          `json:"message"`
		Code    json.RawMessage `json:"code"`
	} `json:"error"`
}

// maxErrorMessage caps how much of an unparseable body is copied into Message
// so that a large HTML error page cannot balloon the error value.
const maxErrorMessage = 2048

// newAPIError builds the typed error for a non-2xx response.
func newAPIError(statusCode int, header http.Header, body []byte) error {
	apiErr := APIError{
		StatusCode: statusCode,
		Body:       body,
	}

	var env errorEnvelope
	if err := json.Unmarshal(body, &env); err == nil && env.Error.Message != "" {
		apiErr.Message = env.Error.Message
		apiErr.Code = env.Error.Code
	} else {
		apiErr.Message = truncate(strings.TrimSpace(string(body)), maxErrorMessage)
	}

	if statusCode == http.StatusTooManyRequests {
		after, _ := parseRetryAfter(header)
		return &RateLimitError{APIError: apiErr, RetryAfter: after}
	}
	return &apiErr
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

// parseRetryAfter reads the wait the server asked for. It understands both
// forms defined by RFC 9110 and the millisecond variant that the official SDK
// also honours:
//
//	Retry-After: 120
//	Retry-After: Fri, 31 Dec 1999 23:59:59 GMT
//	retry-after-ms: 1500
//
// ok is false when no usable header is present. Negative results are clamped to
// zero so a past date never turns into a negative sleep.
func parseRetryAfter(h http.Header) (time.Duration, bool) {
	if h == nil {
		return 0, false
	}

	if v := strings.TrimSpace(h.Get("retry-after-ms")); v != "" {
		if ms, err := strconv.ParseFloat(v, 64); err == nil {
			if ms < 0 {
				ms = 0
			}
			return time.Duration(ms * float64(time.Millisecond)), true
		}
	}

	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0, false
	}

	// Seconds form.
	if secs, err := strconv.ParseFloat(v, 64); err == nil {
		if secs < 0 {
			secs = 0
		}
		return time.Duration(secs * float64(time.Second)), true
	}

	// HTTP-date form.
	for _, layout := range []string{http.TimeFormat, time.RFC1123, time.RFC1123Z, time.RFC850, time.ANSIC} {
		when, err := time.Parse(layout, v)
		if err != nil {
			continue
		}
		if d := time.Until(when); d > 0 {
			return d, true
		}
		return 0, true
	}

	return 0, false
}
