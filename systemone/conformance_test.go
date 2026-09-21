package systemone

// conformance_test.go is independent verification of the frozen contract in
// doc.go, driven by the real golden fixtures in testdata/golden.
//
// It is deliberately written without reusing the helpers in answers_test.go,
// questions_test.go, client_test.go and transport_test.go: the point is to
// re-derive expectations from the raw fixture bytes rather than from the
// implementation's own notion of what they mean. Where a helper here duplicates
// one there, that is the intent — an independent second implementation is what
// makes a disagreement meaningful.
//
// Everything in this file is offline. It needs no network and no credentials.
//
// Known divergences between doc.go and the live service that this file pins
// deliberately (evidence in testdata/golden/PROBES.md):
//
//   - A one-level score rubric is rejected client-side by ValidateQuestions even
//     though the live gateway accepts it. That is a documented policy choice
//     (questions.go); the tests below pin the policy, not the live tolerance.
//   - A 200 response of `{}` decodes to a zero-valued Result with no error. The
//     tests record that rather than assert it is desirable.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const conformanceGoldenDir = "testdata/golden"

// ---------------------------------------------------------------------------
// Golden fixture loading
// ---------------------------------------------------------------------------

// conformanceFixture is one file in testdata/golden. Only request, response and
// status are contractual; note and request_authorized are capture metadata.
type conformanceFixture struct {
	Note              string          `json:"note"`
	Request           json.RawMessage `json:"request"`
	RequestAuthorized *bool           `json:"request_authorized"`
	Status            int             `json:"status"`
	Response          json.RawMessage `json:"response"`
}

// conformanceFixtureRequest is the fixture's request member.
type conformanceFixtureRequest struct {
	Model     string          `json:"model"`
	State     json.RawMessage `json:"state"`
	Questions json.RawMessage `json:"questions"`
}

// The fixtures that are covered. These are listed explicitly rather than
// globbed so that TestConformanceEveryGoldenFixtureIsReplayed can fail when a
// new fixture appears that nothing here exercises.
var conformanceSuccessFixtures = []string{
	"noul_string.json",
	"noul_criteria.json",
	"choice_null_description.json",
	"score_rubric.json",
	"mixed_primitives_object_state.json",
	"state_string.json",
	"state_array.json",
	"chinese_state.json",
}

var conformanceErrorFixtures = []string{
	"error_model_not_found.json",
	"error_bad_question_type.json",
	"error_empty_questions.json",
	"error_no_auth.json",
}

func loadConformanceFixture(t *testing.T, name string) conformanceFixture {
	t.Helper()
	path := filepath.Join(conformanceGoldenDir, name)
	raw, err := os.ReadFile(path)
	require.NoErrorf(t, err, "reading golden fixture %s", path)

	var f conformanceFixture
	require.NoErrorf(t, json.Unmarshal(raw, &f), "parsing golden fixture %s", name)
	require.NotEmptyf(t, f.Response, "fixture %s has no response", name)
	require.NotZerof(t, f.Status, "fixture %s has no status", name)
	return f
}

func (f conformanceFixture) request(t *testing.T) conformanceFixtureRequest {
	t.Helper()
	var r conformanceFixtureRequest
	require.NoError(t, json.Unmarshal(f.Request, &r), "parsing fixture request")
	require.NotEmpty(t, r.Model, "fixture request has no model")
	require.NotEmpty(t, r.Questions, "fixture request has no questions")
	return r
}

// conformanceJSONValue decodes raw JSON into any, keeping numbers as
// json.Number so that two bodies can be compared without float formatting
// differences. Both sides of every comparison go through this function.
func conformanceJSONValue(t *testing.T, raw []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	require.NoErrorf(t, dec.Decode(&v), "decoding JSON value %s", truncateForMessage(raw))
	return v
}

func truncateForMessage(b []byte) string {
	const maxLen = 120
	if len(b) <= maxLen {
		return string(b)
	}
	return string(b[:maxLen]) + "..."
}

// conformanceEntry turns one raw JSON entry into the Entry value a caller would
// have passed. Numbers stay json.Number so the round trip is lossless.
func conformanceEntry(t *testing.T, raw json.RawMessage) Entry {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	require.NoErrorf(t, dec.Decode(&v), "decoding entry %s", truncateForMessage(raw))
	return v
}

// conformanceQuestions reconstructs the Questions value that produced the
// fixture's request, so a replay can be compared against it.
func conformanceQuestions(t *testing.T, raw json.RawMessage) Questions {
	t.Helper()

	var byName map[string]json.RawMessage
	require.NoErrorf(t, json.Unmarshal(raw, &byName), "parsing fixture questions")

	out := make(Questions, len(byName))
	for name, rawQuestion := range byName {
		var disc struct {
			Type         string          `json:"type"`
			Instructions json.RawMessage `json:"instructions"`
			Criteria     json.RawMessage `json:"criteria"`
		}
		require.NoErrorf(t, json.Unmarshal(rawQuestion, &disc), "parsing question %q", name)
		instructions := conformanceEntry(t, disc.Instructions)

		switch disc.Type {
		case typeNoul:
			// criteria present, vs absent entirely, are different requests.
			var criteria *NoulCriteria
			if len(bytes.TrimSpace(disc.Criteria)) > 0 && !bytes.Equal(bytes.TrimSpace(disc.Criteria), []byte("null")) {
				var fields map[string]json.RawMessage
				require.NoErrorf(t, json.Unmarshal(disc.Criteria, &fields), "noul criteria of %q", name)
				criteria = &NoulCriteria{}
				if v, ok := fields["true"]; ok {
					criteria.True = conformanceEntry(t, v)
				}
				if v, ok := fields["false"]; ok {
					criteria.False = conformanceEntry(t, v)
				}
			}
			out[name] = NoulQuestion{Instructions: instructions, Criteria: criteria}

		case typeChoice:
			var fields map[string]json.RawMessage
			require.NoErrorf(t, json.Unmarshal(disc.Criteria, &fields), "choice criteria of %q", name)
			labels := make(map[string]Entry, len(fields))
			for label, v := range fields {
				labels[label] = conformanceEntry(t, v)
			}
			out[name] = ChoiceQuestion{Instructions: instructions, Criteria: labels}

		case typeScore:
			var fields []json.RawMessage
			require.NoErrorf(t, json.Unmarshal(disc.Criteria, &fields), "score criteria of %q", name)
			levels := make([]Entry, len(fields))
			for i, v := range fields {
				levels[i] = conformanceEntry(t, v)
			}
			out[name] = ScoreQuestion{Instructions: instructions, Criteria: levels}

		default:
			t.Fatalf("fixture question %q has unknown type %q", name, disc.Type)
		}
	}
	return out
}

// conformanceReplayServer serves one fixture response and captures the request
// that was sent.
type conformanceReplayServer struct {
	*httptest.Server
	mu        sync.Mutex
	method    string
	path      string
	body      []byte
	requests  int32
	authorize string
}

func newConformanceReplayServer(t *testing.T, status int, body []byte) *conformanceReplayServer {
	t.Helper()

	c := &conformanceReplayServer{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := readAllForTest(r)
		if err != nil {
			t.Errorf("reading request body: %v", err)
		}
		c.mu.Lock()
		c.method, c.path, c.body = r.Method, r.URL.Path, raw
		c.authorize = r.Header.Get("Authorization")
		c.mu.Unlock()
		atomic.AddInt32(&c.requests, 1)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(c.Close)
	return c
}

func readAllForTest(r *http.Request) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r.Body); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (c *conformanceReplayServer) capturedBody() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.body...)
}

func (c *conformanceReplayServer) capturedPath() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.path
}

func (c *conformanceReplayServer) capturedMethod() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.method
}

func (c *conformanceReplayServer) hits() int { return int(atomic.LoadInt32(&c.requests)) }

// newConformanceClient points a client at srv with retries off, so a test can
// never be slowed or confused by the retry policy.
func newConformanceClient(t *testing.T, baseURL, model string) *Client {
	t.Helper()
	c, err := New(
		WithBaseURL(baseURL),
		WithAPIKey("conformance-test-key"),
		WithModel(model),
		WithRetryPolicy(RetryPolicy{MaxRetries: 0}),
	)
	require.NoError(t, err)
	return c
}

// ---------------------------------------------------------------------------
// Independent re-derivation of the expected answers from the fixture bytes
// ---------------------------------------------------------------------------

// conformanceExpectedResult re-derives, from a success response body alone, the
// Result the documented contract says the client must produce.
//
// This is a second implementation of the decode rules, written from doc.go's
// description ("legend" and "probabilities" are objects keyed by the decimal
// score as a string; "score" is an expectation and may be fractional) rather
// than from answers.go. If the two disagree, the fixture is the tie-breaker and
// the disagreement is the finding.
func conformanceExpectedResult(t *testing.T, responseRaw []byte) *Result {
	t.Helper()

	var wire struct {
		Model    string `json:"model"`
		Provider string `json:"provider"`
		ID       string `json:"id"`
		Usage    struct {
			InputTokens  int      `json:"input_tokens"`
			OutputTokens int      `json:"output_tokens"`
			Cost         *float64 `json:"cost"`
		} `json:"usage"`
		Answers map[string]json.RawMessage `json:"answers"`
	}
	require.NoError(t, json.Unmarshal(responseRaw, &wire), "parsing fixture response")

	result := &Result{
		Model:    wire.Model,
		Provider: wire.Provider,
		ID:       wire.ID,
		Usage: Usage{
			InputTokens:  wire.Usage.InputTokens,
			OutputTokens: wire.Usage.OutputTokens,
			Cost:         wire.Usage.Cost,
		},
		Answers: make(map[string]Answer, len(wire.Answers)),
	}

	for name, raw := range wire.Answers {
		var disc struct {
			Type string `json:"type"`
		}
		require.NoErrorf(t, json.Unmarshal(raw, &disc), "answer %q", name)

		switch disc.Type {
		case typeNoul:
			var w struct {
				Noul float64 `json:"noul"`
			}
			require.NoErrorf(t, json.Unmarshal(raw, &w), "noul answer %q", name)
			result.Answers[name] = NoulAnswer{Noul: w.Noul}

		case typeChoice:
			var w struct {
				Choice        string             `json:"choice"`
				Confidence    float64            `json:"confidence"`
				Probabilities map[string]float64 `json:"probabilities"`
			}
			require.NoErrorf(t, json.Unmarshal(raw, &w), "choice answer %q", name)
			result.Answers[name] = ChoiceAnswer{
				Choice:        w.Choice,
				Confidence:    w.Confidence,
				Probabilities: w.Probabilities,
			}

		case typeScore:
			var w struct {
				Score         float64            `json:"score"`
				Confidence    float64            `json:"confidence"`
				Legend        map[string]string  `json:"legend"`
				Probabilities map[string]float64 `json:"probabilities"`
			}
			require.NoErrorf(t, json.Unmarshal(raw, &w), "score answer %q", name)
			result.Answers[name] = ScoreAnswer{
				Score:         w.Score,
				Confidence:    w.Confidence,
				Legend:        conformanceDensifyLegend(w.Legend),
				Probabilities: conformanceDensifyProbabilities(w.Probabilities),
			}

		default:
			t.Fatalf("answer %q has unexpected type %q", name, disc.Type)
		}
	}
	return result
}

// conformanceIndex is the decimal-string key to index rule, re-derived from the
// contract: keys are the decimal score, so anything that is not a plain
// non-negative decimal integer addresses no level.
func conformanceIndex(key string) (int, bool) {
	if key == "" {
		return 0, false
	}
	n, err := strconv.Atoi(key)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func conformanceDensifyLegend(m map[string]string) []string {
	length := 0
	for key := range m {
		if i, ok := conformanceIndex(key); ok && i >= length {
			length = i + 1
		}
	}
	if length == 0 {
		return nil
	}
	out := make([]string, length)
	for key, v := range m {
		if i, ok := conformanceIndex(key); ok {
			out[i] = v
		}
	}
	return out
}

func conformanceDensifyProbabilities(m map[string]float64) []float64 {
	length := 0
	for key := range m {
		if i, ok := conformanceIndex(key); ok && i >= length {
			length = i + 1
		}
	}
	if length == 0 {
		return nil
	}
	out := make([]float64, length)
	for key, v := range m {
		if i, ok := conformanceIndex(key); ok {
			out[i] = v
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// A1 + A2: replay every success fixture and compare both directions
// ---------------------------------------------------------------------------

func TestConformanceGoldenSuccessReplay(t *testing.T) {
	require.NotEmpty(t, conformanceSuccessFixtures)

	for _, name := range conformanceSuccessFixtures {
		t.Run(name, func(t *testing.T) {
			fixture := loadConformanceFixture(t, name)
			require.Equal(t, http.StatusOK, fixture.Status, "success fixture must have status 200")
			req := fixture.request(t)

			srv := newConformanceReplayServer(t, fixture.Status, fixture.Response)
			client := newConformanceClient(t, srv.URL, req.Model)

			state := conformanceEntry(t, req.State)
			questions := conformanceQuestions(t, req.Questions)

			result, err := client.Evaluate(context.Background(), state, questions)
			require.NoErrorf(t, err, "replaying fixture %s", name)
			require.NotNil(t, result)
			require.Equal(t, 1, srv.hits(), "fixture replay must make exactly one request")
			require.Equal(t, http.MethodPost, srv.capturedMethod())
			require.Equal(t, "/systemone", srv.capturedPath(), "the endpoint path is frozen by doc.go")

			// A2: the request the client actually sent must be the fixture's
			// request, compared semantically. Comparing text would fail on Go's
			// randomised map key order.
			sent := conformanceJSONValue(t, srv.capturedBody())
			want := conformanceJSONValue(t, fixture.Request)
			if !reflect.DeepEqual(want, sent) {
				t.Errorf("request body mismatch for %s\nwant: %s\ngot:  %s",
					name, truncateForMessage(fixture.Request), truncateForMessage(srv.capturedBody()))
			}

			// A1: every decoded field must match what the fixture says.
			expected := conformanceExpectedResult(t, fixture.Response)
			assert.Equal(t, expected.Model, result.Model, "Model")
			assert.Equal(t, expected.Provider, result.Provider, "Provider")
			assert.Equal(t, expected.ID, result.ID, "ID")
			assert.Equal(t, expected.Usage, result.Usage, "Usage (Cost must stay a nil pointer when the gateway omits it)")
			assert.Equal(t, expected.Answers, result.Answers, "Answers")

			// The typed accessors must agree with the map, since they are the
			// documented way to read answers.
			for answerName, answer := range expected.Answers {
				switch want := answer.(type) {
				case NoulAnswer:
					got, ok := result.Noul(answerName)
					require.Truef(t, ok, "Noul(%q)", answerName)
					assert.Equal(t, want, got)
					// The type-specific accessors must refuse a mismatched type.
					_, isChoice := result.Choice(answerName)
					assert.Falsef(t, isChoice, "Choice(%q) must not accept a noul answer", answerName)
					_, isScore := result.Score(answerName)
					assert.Falsef(t, isScore, "Score(%q) must not accept a noul answer", answerName)
				case ChoiceAnswer:
					got, ok := result.Choice(answerName)
					require.Truef(t, ok, "Choice(%q)", answerName)
					assert.Equal(t, want, got)
				case ScoreAnswer:
					got, ok := result.Score(answerName)
					require.Truef(t, ok, "Score(%q)", answerName)
					assert.Equal(t, want, got)
					assert.Len(t, got.Probabilities, len(got.Legend),
						"legend and probabilities must densify to the same length")
				}
			}
		})
	}
}

// TestConformanceGoldenScoreFixturesKeepFractionalScore pins the single easiest
// thing to get wrong: "score" is an expectation, not an index.
//
// The expected value is read from the fixture as a json.Number, so this does not
// hardcode the captured literals and survives a re-capture.
func TestConformanceGoldenScoreFixturesKeepFractionalScore(t *testing.T) {
	var sawFractional bool

	for _, name := range conformanceSuccessFixtures {
		t.Run(name, func(t *testing.T) {
			fixture := loadConformanceFixture(t, name)

			var wire struct {
				Answers map[string]struct {
					Type  string      `json:"type"`
					Score json.Number `json:"score"`
				} `json:"answers"`
			}
			require.NoError(t, json.Unmarshal(fixture.Response, &wire))

			expected := conformanceExpectedResult(t, fixture.Response)

			for answerName, raw := range wire.Answers {
				if raw.Type != typeScore {
					continue
				}
				want, err := raw.Score.Float64()
				require.NoError(t, err)

				sa, ok := expected.Answers[answerName].(ScoreAnswer)
				require.Truef(t, ok, "answer %q should be a ScoreAnswer", answerName)

				assert.Equalf(t, want, sa.Score,
					"the fixture's exact score %s must survive decoding unrounded", raw.Score.String())
				if sa.Score != math.Trunc(sa.Score) {
					sawFractional = true
				}
				require.NotEmptyf(t, sa.Legend, "answer %q has a legend", answerName)
				assert.GreaterOrEqual(t, sa.Score, 0.0)
				assert.LessOrEqual(t, sa.Score, float64(len(sa.Legend)),
					"an expectation outside the rubric would be a fixture or decoder bug")

				// And the same value must come out of the client's own decode
				// path, not just the test's re-derivation. The answer name is
				// reused so the reply matches; the questions are irrelevant to
				// decoding.
				srv := newConformanceReplayServer(t, fixture.Status, fixture.Response)
				client := newConformanceClient(t, srv.URL, "m")
				result, err := client.Evaluate(context.Background(), "x", Questions{
					answerName: ScoreQuestion{
						Instructions: "i",
						Criteria:     []Entry{"0: a", "1: b", "2: c", "3: d"},
					},
				})
				require.NoError(t, err)
				got, ok := result.Score(answerName)
				require.Truef(t, ok, "Score(%q) from the live decode path", answerName)
				assert.Equalf(t, want, got.Score, "decoded score must not be rounded into an index")
				assert.Equal(t, sa, got)
			}
		})
	}

	assert.True(t, sawFractional,
		"no captured score was fractional, so the rounding guard proves nothing; re-capture a rubric case")
}

// TestConformanceEveryGoldenFixtureIsReplayed is a tripwire: adding a fixture
// without teaching the suite about it must not silently pass.
func TestConformanceEveryGoldenFixtureIsReplayed(t *testing.T) {
	covered := make(map[string]bool)
	for _, n := range conformanceSuccessFixtures {
		covered[n] = true
	}
	for _, n := range conformanceErrorFixtures {
		covered[n] = true
	}

	entries, err := os.ReadDir(conformanceGoldenDir)
	require.NoError(t, err)

	var unreplayed, missing []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if !covered[e.Name()] {
			unreplayed = append(unreplayed, e.Name())
		}
	}
	for name := range covered {
		if _, err := os.Stat(filepath.Join(conformanceGoldenDir, name)); err != nil {
			missing = append(missing, name)
		}
	}

	assert.Emptyf(t, unreplayed,
		"golden fixtures exist that no conformance test replays; add them to conformanceSuccessFixtures or conformanceErrorFixtures")
	assert.Emptyf(t, missing, "fixtures listed by the suite are missing from disk")
	assert.Len(t, covered, 12, "the reviewed fixture set is 12 files")
}

// TestConformanceGoldenFixturesContainNoCredentials guards the rule that the
// captured snapshots never carry the test key or an Authorization header value.
func TestConformanceGoldenFixturesContainNoCredentials(t *testing.T) {
	// Built by concatenation so that this test file itself does not contain the
	// literal the repository-wide audit greps for.
	keyPrefix := "sk-" + "or-v1"
	keyShape := regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`)
	bearerShape := regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/-]{20,}`)

	entries, err := os.ReadDir(conformanceGoldenDir)
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(conformanceGoldenDir, e.Name())
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		text := string(raw)

		assert.NotContainsf(t, text, keyPrefix, "%s leaks a gateway API key", e.Name())
		assert.Emptyf(t, keyShape.FindString(text), "%s leaks a key-shaped token", e.Name())
		assert.Emptyf(t, bearerShape.FindString(text), "%s leaks a Bearer token value", e.Name())
	}
}

// ---------------------------------------------------------------------------
// A3: typed errors from the real error fixtures
// ---------------------------------------------------------------------------

func TestConformanceGoldenErrorReplay(t *testing.T) {
	for _, name := range conformanceErrorFixtures {
		t.Run(name, func(t *testing.T) {
			fixture := loadConformanceFixture(t, name)
			require.GreaterOrEqualf(t, fixture.Status, 400, "error fixture %s must be non-2xx", name)

			srv := newConformanceReplayServer(t, fixture.Status, fixture.Response)
			client := newConformanceClient(t, srv.URL, "typesafe/jev-1.13")

			_, err := client.Evaluate(context.Background(), "state", Questions{
				"q": NoulQuestion{Instructions: "Is this a test?", Criteria: &NoulCriteria{True: "yes", False: "no"}},
			})
			require.Error(t, err)

			// The task description used to say "assert 400". The live gateway
			// returns 400 where the official docs say 422, so only the class of
			// the response is asserted, never a literal status.
			assert.ErrorIs(t, err, ErrAPI, "every non-2xx must match ErrAPI")
			assert.False(t, errors.Is(err, ErrRateLimit), "these fixtures are not 429")

			var apiErr *APIError
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, fixture.Status, apiErr.StatusCode)
			assert.GreaterOrEqual(t, apiErr.StatusCode, 400)
			assert.Less(t, apiErr.StatusCode, 600)

			// The fixture's own error object is the expectation.
			var envelope struct {
				Error struct {
					Message string          `json:"message"`
					Code    json.RawMessage `json:"code"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(fixture.Response, &envelope), "fixture error envelope")
			require.NotEmpty(t, envelope.Error.Message, "fixture carries a message")

			assert.Equal(t, envelope.Error.Message, apiErr.Message,
				"the server's message is the only diagnostic; it must be preserved verbatim")
			assert.JSONEq(t, string(envelope.Error.Code), string(apiErr.Code), "the code field is raw JSON")
			assert.Equal(t, []byte(fixture.Response), apiErr.Body, "the raw body must be kept")
		})
	}
}

// TestConformanceRateLimitCarriesRetryAfterFromEveryHeaderForm covers the 429
// path, which no golden fixture exercises because it cannot be provoked on
// demand against a live service.
func TestConformanceRateLimitCarriesRetryAfterFromEveryHeaderForm(t *testing.T) {
	body := []byte(`{"error":{"message":"slow down","code":429}}`)

	cases := []struct {
		name    string
		header  http.Header
		want    time.Duration
		wantAny bool
	}{
		{name: "seconds", header: http.Header{"Retry-After": {"2"}}, want: 2 * time.Second, wantAny: true},
		{name: "fractional seconds", header: http.Header{"Retry-After": {"1.5"}}, want: 1500 * time.Millisecond, wantAny: true},
		{name: "milliseconds", header: http.Header{"Retry-After-Ms": {"1500"}}, want: 1500 * time.Millisecond, wantAny: true},
		{name: "absent", header: http.Header{}, want: 0, wantAny: false},
		{name: "garbage", header: http.Header{"Retry-After": {"soon"}}, want: 0, wantAny: false},
		{name: "negative clamped", header: http.Header{"Retry-After": {"-5"}}, want: 0, wantAny: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for k, vs := range tc.header {
					for _, v := range vs {
						w.Header().Add(k, v)
					}
				}
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write(body)
			}))
			t.Cleanup(srv.Close)

			client := newConformanceClient(t, srv.URL, "m")
			_, err := client.Evaluate(context.Background(), "s", Questions{"q": NoulQuestion{Instructions: "i"}})
			require.Error(t, err)

			assert.ErrorIs(t, err, ErrRateLimit)
			assert.ErrorIs(t, err, ErrAPI, "a rate limit is still an API error")

			var rateLimit *RateLimitError
			require.ErrorAs(t, err, &rateLimit)
			assert.Equal(t, http.StatusTooManyRequests, rateLimit.StatusCode)
			assert.Equal(t, tc.want, rateLimit.RetryAfter)

			// The custom As method must make the embedded *APIError reachable,
			// otherwise a caller cannot handle "any HTTP error" uniformly.
			var apiErr *APIError
			require.ErrorAs(t, err, &apiErr, "*RateLimitError.As must expose the embedded *APIError")
			assert.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
			assert.Equal(t, "slow down", apiErr.Message)
		})
	}
}

// TestConformanceRateLimitHTTPDateRetryAfter covers the date form separately so
// the table above can stay clock-independent.
func TestConformanceRateLimitHTTPDateRetryAfter(t *testing.T) {
	when := time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", when)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"slow down","code":429}}`))
	}))
	t.Cleanup(srv.Close)

	client := newConformanceClient(t, srv.URL, "m")
	_, err := client.Evaluate(context.Background(), "s", Questions{"q": NoulQuestion{Instructions: "i"}})
	require.Error(t, err)

	var rateLimit *RateLimitError
	require.ErrorAs(t, err, &rateLimit)
	assert.InDelta(t, 90*time.Second, rateLimit.RetryAfter, float64(5*time.Second))
}

// ---------------------------------------------------------------------------
// A4: hostile and malformed success bodies
// ---------------------------------------------------------------------------

func TestConformanceMalformedSuccessBodies(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		body        string
		wantDecode  bool // ErrDecode expected
		wantAnswers int
		note        string
	}{
		{name: "empty body", status: 200, body: "", wantDecode: true},
		{name: "whitespace only", status: 200, body: "   \n\t ", wantDecode: true},
		{name: "truncated json", status: 200, body: `{"model":"x","answers":{`, wantDecode: true},
		{name: "not json at all", status: 200, body: `<html>502 Bad Gateway</html>`, wantDecode: true},
		{name: "json null", status: 200, body: `null`, wantAnswers: 0,
			note: "decodes to a zero Result with no error"},
		{name: "empty object", status: 200, body: `{}`, wantAnswers: 0,
			note: "decodes to a zero Result with no error: a caller cannot tell this from a real empty response"},
		{name: "answers null", status: 200, body: `{"answers":null}`, wantAnswers: 0},
		{name: "answers empty", status: 200, body: `{"answers":{}}`, wantAnswers: 0},
		{name: "answer null", status: 200, body: `{"answers":{"x":null}}`, wantAnswers: 0,
			note: "a null answer has no type, so it is skipped rather than fatal"},
		{name: "future answer type", status: 200, body: `{"answers":{"x":{"type":"future_type"}}}`, wantAnswers: 0,
			note: "unknown answer types are skipped, not fatal"},
		{name: "missing type", status: 200, body: `{"answers":{"x":{"noul":0.5}}}`, wantAnswers: 0},
		{name: "unknown type beside known", status: 200,
			body:        `{"answers":{"a":{"type":"future_type"},"b":{"type":"noul","noul":0.5}}}`,
			wantAnswers: 1, note: "the known answer must survive the unknown one"},
		{name: "answers is an array", status: 200, body: `{"answers":[]}`, wantDecode: true},
		{name: "model is a number", status: 200, body: `{"model":5,"answers":{}}`, wantDecode: true},
		{name: "usage is a string", status: 200, body: `{"usage":"none","answers":{}}`, wantDecode: true},
		{name: "noul is a string", status: 200, body: `{"answers":{"x":{"type":"noul","noul":"0.5"}}}`, wantDecode: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newConformanceReplayServer(t, tc.status, []byte(tc.body))
			client := newConformanceClient(t, srv.URL, "m")

			// A panic here fails the test on its own; there is nothing to catch.
			result, err := client.Evaluate(context.Background(), "s", Questions{"q": NoulQuestion{Instructions: "i"}})

			if tc.wantDecode {
				require.Errorf(t, err, "expected a decode failure (%s)", tc.note)
				assert.ErrorIs(t, err, ErrDecode)
				assert.Nil(t, result)
				return
			}
			require.NoErrorf(t, err, "expected a successful decode (%s)", tc.note)
			require.NotNil(t, result)
			assert.Len(t, result.Answers, tc.wantAnswers)
		})
	}
}

// TestConformanceDeeplyNestedBodyDoesNotPanic is the stack-exhaustion probe.
func TestConformanceDeeplyNestedBodyDoesNotPanic(t *testing.T) {
	const depth = 50000
	nested := strings.Repeat("[", depth) + strings.Repeat("]", depth)
	body := `{"answers":{"x":{"type":"noul","noul":` + nested + `}}}`

	srv := newConformanceReplayServer(t, 200, []byte(body))
	client := newConformanceClient(t, srv.URL, "m")

	// encoding/json rejects over-deep input with an error rather than
	// recursing until the stack dies; either an error or a decode is
	// acceptable, but a panic is not.
	_, err := client.Evaluate(context.Background(), "s", Questions{"q": NoulQuestion{Instructions: "i"}})
	if err != nil {
		assert.True(t, errors.Is(err, ErrDecode), "unexpected error class: %v", err)
	}
}

// TestConformanceHugeBodyIsDecoded covers the "very large payload" case.
func TestConformanceHugeBodyIsDecoded(t *testing.T) {
	const answers = 60000

	var body bytes.Buffer
	body.WriteString(`{"model":"m","usage":{"input_tokens":1,"output_tokens":1},"answers":{`)
	for i := 0; i < answers; i++ {
		if i > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `"q%d":{"type":"noul","noul":0.5}`, i)
	}
	body.WriteString(`}}`)
	require.Greater(t, body.Len(), 1<<20, "this test is only meaningful for a multi-megabyte body")

	srv := newConformanceReplayServer(t, 200, body.Bytes())
	client := newConformanceClient(t, srv.URL, "m")

	result, err := client.Evaluate(context.Background(), "s", Questions{"q": NoulQuestion{Instructions: "i"}})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Answers, answers)
}

// ---------------------------------------------------------------------------
// A5: the Client must be safe to reuse concurrently
// ---------------------------------------------------------------------------

// TestConformanceClientIsSafeForConcurrentReuse runs 100 concurrent evaluations
// through one Client. The server echoes the caller's index back, so any
// cross-talk between goroutines (shared buffers, shared headers, a mutated
// per-call config) shows up as a wrong answer rather than merely as a race.
func TestConformanceClientIsSafeForConcurrentReuse(t *testing.T) {
	const goroutines = 100

	var seenModels sync.Map // index -> model the server observed

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := readAllForTest(r)
		if err != nil {
			t.Errorf("reading body: %v", err)
			return
		}
		var payload struct {
			Model string `json:"model"`
			State string `json:"state"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Errorf("request body is not the expected shape: %v", err)
			return
		}
		index, err := strconv.Atoi(strings.TrimPrefix(payload.State, "state-"))
		if err != nil {
			t.Errorf("state %q did not carry the caller index", payload.State)
			return
		}
		seenModels.Store(index, payload.Model)

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"model":"resolved","answers":{"echo":{"type":"noul","noul":%d}}}`, index)
	}))
	t.Cleanup(srv.Close)

	client, err := New(
		WithBaseURL(srv.URL),
		WithAPIKey("conformance-test-key"),
		WithRetryPolicy(RetryPolicy{MaxRetries: 0}),
	)
	require.NoError(t, err)

	var wg sync.WaitGroup
	results := make([]*Result, goroutines)
	errs := make([]error, goroutines)
	models := make([]string, goroutines)

	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		models[i] = fmt.Sprintf("model-%d", i)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // maximise overlap
			results[i], errs[i] = client.Evaluate(
				context.Background(),
				fmt.Sprintf("state-%d", i),
				Questions{"echo": NoulQuestion{Instructions: "echo"}},
				WithCallModel(models[i]),
			)
		}(i)
	}
	close(start)
	wg.Wait()

	for i := 0; i < goroutines; i++ {
		require.NoErrorf(t, errs[i], "goroutine %d", i)
		require.NotNilf(t, results[i], "goroutine %d", i)
		got, ok := results[i].Noul("echo")
		require.Truef(t, ok, "goroutine %d", i)
		assert.Equalf(t, float64(i), got.Noul, "goroutine %d got another goroutine's answer", i)

		seen, ok := seenModels.Load(i)
		require.Truef(t, ok, "the server never saw goroutine %d", i)
		assert.Equalf(t, models[i], seen, "goroutine %d had its per-call model clobbered", i)
	}
}

// ---------------------------------------------------------------------------
// C: adversarial decoding
// ---------------------------------------------------------------------------

// scoreBody wraps legend and probabilities objects into a well-formed score
// answer so each case below can vary only the objects under test.
func scoreBody(score, confidence, legend, probabilities string) string {
	return fmt.Sprintf(
		`{"model":"m","answers":{"q":{"type":"score","score":%s,"confidence":%s,"legend":%s,"probabilities":%s}}}`,
		score, confidence, legend, probabilities)
}

func TestConformanceScoreAnswerAdversarialKeys(t *testing.T) {
	cases := []struct {
		name          string
		body          string
		wantLegend    []string
		wantProbs     []float64
		wantDecodeErr bool
		note          string
	}{
		{
			name:       "keys out of order",
			body:       scoreBody(`1.0`, `0.9`, `{"3":"severe","0":"none","2":"serious","1":"mild"}`, `{"3":0,"0":0.76,"2":0,"1":0.24}`),
			wantLegend: []string{"none", "mild", "serious", "severe"},
			wantProbs:  []float64{0.76, 0.24, 0, 0},
			note:       "indexed by the numeric key, not by encounter order",
		},
		{
			name:       "missing middle key",
			body:       scoreBody(`1.0`, `0.9`, `{"0":"none","2":"serious"}`, `{"0":0.5,"2":0.5}`),
			wantLegend: []string{"none", "", "serious"},
			wantProbs:  []float64{0.5, 0, 0.5},
			note:       "the gap is filled, not skipped",
		},
		{
			name:       "non-numeric keys ignored",
			body:       scoreBody(`1.0`, `0.9`, `{"0":"none","x":"bogus","1":"mild","-1":"neg","1.5":"frac"}`, `{"0":0.5,"x":9,"1":0.5}`),
			wantLegend: []string{"none", "mild"},
			wantProbs:  []float64{0.5, 0.5},
			note:       "a key that is not a plain decimal integer addresses no level",
		},
		{
			name:       "empty objects",
			body:       scoreBody(`0`, `0`, `{}`, `{}`),
			wantLegend: nil,
			wantProbs:  nil,
			note:       "nil slices, both of them, and no error",
		},
		{
			name:       "null legend entries",
			body:       scoreBody(`1.0`, `0.9`, `{"0":null,"1":"mild"}`, `{"0":1,"1":0}`),
			wantLegend: []string{"", "mild"},
			wantProbs:  []float64{1, 0},
		},
		{
			name:       "index at the cap",
			body:       scoreBody(`1.0`, `0.9`, `{"0":"a","4096":"b"}`, `{"0":1,"4096":0}`),
			wantLegend: append(append([]string{"a"}, make([]string, 4095)...), "b"),
			wantProbs:  append(append([]float64{1}, make([]float64, 4095)...), 0),
			note:       "maxScoreIndex is inclusive",
		},
		{
			name:       "index above the cap ignored",
			body:       scoreBody(`1.0`, `0.9`, `{"0":"a","4097":"b","99999":"c"}`, `{"0":1}`),
			wantLegend: []string{"a"},
			wantProbs:  []float64{1},
			note:       "an absurd index must not drive a gigantic allocation",
		},
		{
			name:          "probability is not a number",
			body:          scoreBody(`1.0`, `0.9`, `{"0":"a"}`, `{"0":"NaN"}`),
			wantDecodeErr: true,
			note:          "unlike an unknown key, an uninterpretable value is fatal",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newConformanceReplayServer(t, 200, []byte(tc.body))
			client := newConformanceClient(t, srv.URL, "m")

			result, err := client.Evaluate(context.Background(), "s", Questions{
				"q": ScoreQuestion{Instructions: "i", Criteria: []Entry{"0: a", "1: b"}},
			})

			if tc.wantDecodeErr {
				require.Errorf(t, err, "%s", tc.note)
				assert.ErrorIs(t, err, ErrDecode)
				return
			}
			require.NoErrorf(t, err, "%s", tc.note)
			got, ok := result.Score("q")
			require.True(t, ok)
			assert.Equalf(t, tc.wantLegend, got.Legend, "legend (%s)", tc.note)
			assert.Equalf(t, tc.wantProbs, got.Probabilities, "probabilities (%s)", tc.note)
		})
	}
}

// TestConformanceScoreDuplicateIndexIsDeterministic pins the one case where two
// distinct keys address the same level ("1" and "01"). The result is arbitrary
// but must not vary between runs.
func TestConformanceScoreDuplicateIndexIsDeterministic(t *testing.T) {
	body := scoreBody(`1.0`, `0.9`, `{"1":"one","01":"zero-one"}`, `{"1":0.25,"01":0.75}`)

	var first *ScoreAnswer
	for run := 0; run < 20; run++ {
		srv := newConformanceReplayServer(t, 200, []byte(body))
		client := newConformanceClient(t, srv.URL, "m")
		result, err := client.Evaluate(context.Background(), "s", Questions{
			"q": ScoreQuestion{Instructions: "i", Criteria: []Entry{"0: a", "1: b"}},
		})
		require.NoError(t, err)
		got, ok := result.Score("q")
		require.True(t, ok)

		if first == nil {
			first = &got
			// Both keys address index 1, so the slice must be exactly 2 long.
			assert.Len(t, got.Legend, 2)
			assert.Len(t, got.Probabilities, 2)
			continue
		}
		require.Equal(t, *first, got, "duplicate-index decoding must not vary between runs")
	}
}

// TestConformanceChoiceAnswerAcceptsInconsistentProbabilities records what
// happens when the server's "choice" disagrees with its "probabilities". The
// client is a transport, not a validator, so it passes the payload through; the
// point of the test is to make that a decision rather than an accident.
func TestConformanceChoiceAnswerAcceptsInconsistentProbabilities(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "chosen label has the lower probability",
			body: `{"model":"m","answers":{"q":{"type":"choice","choice":"a","confidence":0.1,
			        "probabilities":{"a":0.1,"b":0.9}}}}`,
		},
		{
			name: "chosen label is absent from probabilities",
			body: `{"model":"m","answers":{"q":{"type":"choice","choice":"ghost","confidence":0.5,
			        "probabilities":{"a":0.5,"b":0.5}}}}`,
		},
		{
			name: "probabilities absent entirely",
			body: `{"model":"m","answers":{"q":{"type":"choice","choice":"a","confidence":0.5}}}`,
		},
		{
			name: "probabilities do not sum to one",
			body: `{"model":"m","answers":{"q":{"type":"choice","choice":"a","confidence":1,
			        "probabilities":{"a":5,"b":-3}}}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newConformanceReplayServer(t, 200, []byte(tc.body))
			client := newConformanceClient(t, srv.URL, "m")

			result, err := client.Evaluate(context.Background(), "s", Questions{
				"q": ChoiceQuestion{Instructions: "i", Criteria: map[string]Entry{"a": "A", "b": "B"}},
			})
			// Documented behaviour: the payload is passed through untouched, so
			// these are all accepted. A caller that needs the invariant must
			// check it.
			require.NoError(t, err)
			got, ok := result.Choice("q")
			require.True(t, ok)
			assert.NotEmpty(t, got.Choice, "the chosen label is never silently rewritten")
		})
	}
}

// ---------------------------------------------------------------------------
// C: cancellation and timeouts
// ---------------------------------------------------------------------------

func TestConformanceCancelDuringRetryReturnsImmediately(t *testing.T) {
	var hits int32
	release := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(func() { close(release); srv.Close() })

	// A long first backoff, so cancellation lands squarely inside it.
	policy := RetryPolicy{
		MaxRetries:            20,
		InitialBackoff:        5 * time.Second,
		MaxBackoff:            5 * time.Second,
		BackoffJitter:         0,
		RetryStatuses:         []int{500},
		RetryConnectionErrors: true,
		RetryTimeoutErrors:    true,
	}
	client, err := New(WithBaseURL(srv.URL), WithAPIKey("k"), WithRetryPolicy(policy))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err = client.Evaluate(ctx, "s", Questions{"q": NoulQuestion{Instructions: "i"}})
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAborted)
	assert.Lessf(t, elapsed, 2*time.Second,
		"cancellation must interrupt the backoff sleep, not wait it out (took %s)", elapsed)
	assert.LessOrEqualf(t, atomic.LoadInt32(&hits), int32(2),
		"no further attempt may be made after the caller cancels")
}

func TestConformancePreCancelledContextSendsNoRequest(t *testing.T) {
	srv := newConformanceReplayServer(t, 200, []byte(`{"answers":{}}`))
	client := newConformanceClient(t, srv.URL, "m")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := client.Evaluate(ctx, "s", Questions{"q": NoulQuestion{Instructions: "i"}})
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAborted)
	assert.Less(t, elapsed, time.Second)
	assert.Zero(t, srv.hits(), "an already-cancelled context must not reach the network")
}

func TestConformanceTotalTimeoutIsEnforcedAcrossRetries(t *testing.T) {
	var hits int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		select {
		case <-release:
		case <-time.After(30 * time.Second):
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })

	client, err := New(
		WithBaseURL(srv.URL),
		WithAPIKey("k"),
		WithTimeout(0), // no per-attempt deadline: the total budget is what is under test
		WithTotalTimeout(120*time.Millisecond),
		WithRetryPolicy(RetryPolicy{
			MaxRetries: 5, InitialBackoff: 50 * time.Millisecond, MaxBackoff: 50 * time.Millisecond,
			RetryConnectionErrors: true, RetryTimeoutErrors: true,
		}),
	)
	require.NoError(t, err)

	start := time.Now()
	_, err = client.Evaluate(context.Background(), "s", Questions{"q": NoulQuestion{Instructions: "i"}})
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTimeout, "an overall budget must surface as a timeout")
	assert.Lessf(t, elapsed, 3*time.Second, "WithTotalTimeout must bound the whole call (took %s)", elapsed)
}

// ---------------------------------------------------------------------------
// Request-side contract: escaping, and the noul criteria omission
// ---------------------------------------------------------------------------

// TestConformanceRequestBodyIsNotHTMLEscaped is the regression defence for the
// marshalStringifyTolerant change. Go's encoding/json writes <, > and & as
// \u003c, \u003e and \u0026; JSON.stringify does not, and this API's reference
// client is JavaScript.
//
// Both marshal paths are exercised: questions go through marshalQuestions, and
// state plus the outer payload go through marshalStringifyTolerant. A fix
// applied to only one of them passes a narrow test and fails here.
func TestConformanceRequestBodyIsNotHTMLEscaped(t *testing.T) {
	// No double quotes: they would be escaped as \" inside the JSON string and
	// the verbatim-substring assertion below would fail for the wrong reason.
	const markup = `<script>alert(1)</script></div> & <b>bold</b> 100% > 50%`

	type payload struct {
		Body string `json:"body"`
	}

	cases := []struct {
		name      string
		state     any
		questions Questions
	}{
		{
			name:      "state as a string",
			state:     markup,
			questions: Questions{"q": NoulQuestion{Instructions: "i"}},
		},
		{
			name:      "state as an object",
			state:     map[string]any{"body": markup, "nested": []any{markup, map[string]any{"deep": markup}}},
			questions: Questions{"q": NoulQuestion{Instructions: "i"}},
		},
		{
			name:      "state as a struct",
			state:     payload{Body: markup},
			questions: Questions{"q": NoulQuestion{Instructions: "i"}},
		},
		{
			name:  "noul instructions and criteria",
			state: "plain",
			questions: Questions{"q": NoulQuestion{
				Instructions: markup,
				Criteria:     &NoulCriteria{True: markup, False: markup},
			}},
		},
		{
			name:  "choice instructions and criteria",
			state: "plain",
			questions: Questions{"q": ChoiceQuestion{
				Instructions: markup,
				Criteria:     map[string]Entry{"a": markup, "b": nil},
			}},
		},
		{
			name:  "score instructions and criteria",
			state: "plain",
			questions: Questions{"q": ScoreQuestion{
				Instructions: markup,
				Criteria:     []Entry{markup, "low", map[string]any{"k": markup}},
			}},
		},
		{
			name:      "non-ascii is passed through as utf-8",
			state:     "被重复收费，而且应用打不开。",
			questions: Questions{"q": NoulQuestion{Instructions: "中文指令 <>&"}},
		},
	}

	// Negative control: prove the escape sequences searched for below are the
	// ones encoding/json actually produces. Without this, a typo in a needle
	// would make the whole test silently vacuous.
	escapedByDefault, err := json.Marshal(markup)
	require.NoError(t, err)
	require.Contains(t, string(escapedByDefault), `\u003c`,
		"negative control failed: encoding/json no longer escapes '<', so the assertions below prove nothing")
	require.Contains(t, string(escapedByDefault), `\u0026`,
		"negative control failed: encoding/json no longer escapes '&', so the assertions below prove nothing")

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newConformanceReplayServer(t, 200, []byte(`{"answers":{}}`))
			client := newConformanceClient(t, srv.URL, "m")

			_, err := client.Evaluate(context.Background(), tc.state, tc.questions)
			require.NoError(t, err)

			body := srv.capturedBody()
			require.NotEmpty(t, body)

			// The JSON must still be well formed.
			assert.True(t, json.Valid(body), "body must remain valid JSON: %s", truncateForMessage(body))

			for _, escaped := range []string{`\u003c`, `\u003e`, `\u0026`, `\u003C`, `\u003E`, `\u0026`} {
				assert.NotContainsf(t, string(body), escaped,
					"request body contains the Go-specific HTML escape %s; JSON.stringify would not emit it", escaped)
			}

			if tc.name == "non-ascii is passed through as utf-8" {
				assert.Contains(t, string(body), "被重复收费，而且应用打不开。",
					"non-ASCII must be sent as raw UTF-8, not \\u escapes")
				return
			}
			assert.Containsf(t, string(body), markup,
				"the literal markup must appear in the body: %s", truncateForMessage(body))
		})
	}
}

// TestConformanceNoulCriteriaIsOmittedOnTheWire walks the real golden fixture
// that has no criteria at all and pins that the client does not invent
// "criteria":null, which the live service rejects with HTTP 400.
func TestConformanceNoulCriteriaIsOmittedOnTheWire(t *testing.T) {
	fixture := loadConformanceFixture(t, "noul_string.json")
	req := fixture.request(t)

	// First: the fixture itself must genuinely lack the key. If a re-capture
	// ever adds it, this test's premise is gone and that must be visible.
	var fixtureQuestions map[string]map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(req.Questions, &fixtureQuestions))
	require.Len(t, fixtureQuestions, 1)
	for name, question := range fixtureQuestions {
		_, hasCriteria := question["criteria"]
		require.Falsef(t, hasCriteria,
			"fixture %s question %q gained a criteria key; the premise of this test changed", "noul_string.json", name)
		_, hasInstructions := question["instructions"]
		require.Truef(t, hasInstructions, "instructions is always written, even as null")
	}

	questions := conformanceQuestions(t, req.Questions)

	// The reconstructed Go value must have nil criteria, not an empty struct.
	for name, question := range questions {
		noul, ok := question.(NoulQuestion)
		require.Truef(t, ok, "fixture question %q is a %T", name, question)
		require.Nilf(t, noul.Criteria, "fixture question %q must reconstruct with nil criteria", name)
	}

	srv := newConformanceReplayServer(t, fixture.Status, fixture.Response)
	client := newConformanceClient(t, srv.URL, req.Model)

	_, err := client.Evaluate(context.Background(), conformanceEntry(t, req.State), questions)
	require.NoError(t, err)

	var sentEnvelope struct {
		Questions map[string]map[string]json.RawMessage `json:"questions"`
	}
	require.NoError(t, json.Unmarshal(srv.capturedBody(), &sentEnvelope))
	require.Len(t, sentEnvelope.Questions, 1)

	for name, question := range sentEnvelope.Questions {
		require.NotContainsf(t, question, "criteria",
			"question %q sent a criteria key for a question that has none; the service rejects \"criteria\":null with HTTP 400", name)
		assert.Containsf(t, question, "instructions", "instructions is always present")
	}

	// The whole request must still equal the fixture's.
	assert.True(t, reflect.DeepEqual(
		conformanceJSONValue(t, fixture.Request),
		conformanceJSONValue(t, srv.capturedBody())),
		"reconstructed request must equal the captured one")
}

// TestConformanceNoulNilInstructionsStillWritten pins one half of the rule:
// "instructions" is unconditional and is written as null when unset, whereas
// "criteria" is omitted entirely when nil -- never null, which the host rejects.
//
// It deliberately does NOT go through a non-nil NoulCriteria with an unset side
// any more. That shape is now refused locally by ValidateQuestions, because the
// verified host answers it with HTTP 400; see
// TestConformanceNoulCriteriaWithNilSideIsRejectedLocally. The marshaller-level
// shape of a partial criteria is still covered, without going through Evaluate,
// by TestConformancePartialNoulCriteriaShape.
func TestConformanceNoulNilInstructionsStillWritten(t *testing.T) {
	srv := newConformanceReplayServer(t, 200, []byte(`{"answers":{}}`))
	client := newConformanceClient(t, srv.URL, "m")

	_, err := client.Evaluate(context.Background(), "s", Questions{
		"bare": NoulQuestion{},
	})
	require.NoError(t, err)

	var envelope struct {
		Questions map[string]map[string]json.RawMessage `json:"questions"`
	}
	require.NoError(t, json.Unmarshal(srv.capturedBody(), &envelope))

	bare := envelope.Questions["bare"]
	require.Contains(t, bare, "instructions")
	assert.Equal(t, "null", string(bytes.TrimSpace(bare["instructions"])),
		"an unset instructions is written as null, matching the official SDK's builder")
	assert.NotContains(t, bare, "criteria", "nil criteria must be omitted, never null")
}

// TestConformanceValidateQuestionsPolicy pins the request-side policy,
// including the deliberate divergence from the live gateway.
func TestConformanceValidateQuestionsPolicy(t *testing.T) {
	cases := []struct {
		name string
		set  Questions
		ok   bool
	}{
		{name: "empty set", set: Questions{}, ok: false},
		{name: "nil set", set: nil, ok: false},
		{name: "noul without criteria", set: Questions{"q": NoulQuestion{Instructions: "i"}}, ok: true},
		{name: "choice with one label", set: Questions{"q": ChoiceQuestion{Criteria: map[string]Entry{"a": nil}}}, ok: true},
		{name: "choice with no criteria", set: Questions{"q": ChoiceQuestion{}}, ok: false},
		{name: "score with two levels", set: Questions{"q": ScoreQuestion{Criteria: []Entry{"a", "b"}}}, ok: true},
		{name: "score with ten levels", set: Questions{"q": ScoreQuestion{Criteria: make([]Entry, 10)}}, ok: true},
		{name: "score with eleven levels", set: Questions{"q": ScoreQuestion{Criteria: make([]Entry, 11)}}, ok: false},
		{name: "nil question", set: Questions{"q": nil}, ok: false},
		{name: "typed nil question", set: Questions{"q": (*NoulQuestion)(nil)}, ok: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateQuestions(tc.set)
			if tc.ok {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrValidation)
		})
	}

	// The one-level rubric is the deliberate divergence: the live gateway
	// accepts it (PROBES.md), this client refuses it with a documented reason.
	t.Run("one level score is refused as a policy choice", func(t *testing.T) {
		err := ValidateQuestions(Questions{"q": ScoreQuestion{Criteria: []Entry{"only"}}})
		require.Error(t, err, "one-level rubrics are refused on purpose; see ValidateQuestions' doc comment")
		assert.ErrorIs(t, err, ErrValidation)
		assert.Contains(t, err.Error(), "noul", "the error should point the caller at the alternative")
	})
}

func TestConformanceEvaluateRejectsUnmarshalableStateWithoutNetwork(t *testing.T) {
	cases := []struct {
		name  string
		state any
	}{
		{name: "channel", state: make(chan int)},
		{name: "func", state: func() {}},
		{name: "NaN", state: math.NaN()},
		{name: "positive infinity", state: math.Inf(1)},
		{name: "complex number", state: complex(1, 2)},
		// Integer-keyed maps are legal in encoding/json and are therefore
		// deliberately not in this list; a struct key has no encoder.
		{name: "map with a struct key", state: map[struct{ A int }]string{{A: 1}: "a"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newConformanceReplayServer(t, 200, []byte(`{"answers":{}}`))
			client := newConformanceClient(t, srv.URL, "m")

			_, err := client.Evaluate(context.Background(), tc.state, Questions{"q": NoulQuestion{Instructions: "i"}})
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrValidation)
			assert.Zero(t, srv.hits(), "an unmarshalable request must never reach the network")
		})
	}
}

// ---------------------------------------------------------------------------
// Endpoint and header contract
// ---------------------------------------------------------------------------

func TestConformanceEndpointAndHeaders(t *testing.T) {
	srv := newConformanceReplayServer(t, 200, []byte(`{"answers":{}}`))

	// A base URL with a trailing slash must not produce "//systemone".
	client := newConformanceClient(t, srv.URL+"/", "m")
	_, err := client.Evaluate(context.Background(), "s", Questions{"q": NoulQuestion{Instructions: "i"}})
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, srv.capturedMethod())
	assert.Equal(t, "/systemone", srv.capturedPath())
	assert.Equal(t, "Bearer conformance-test-key", srv.authorize)
}

// TestConformanceCallHeadersCannotAddASecondAuthorization checks the promise in
// WithCallHeaders' and WithHeader's documentation: caller-supplied headers
// "cannot override Authorization, Content-Type or Accept".
//
// The existing test for this reads r.Header.Get, which returns only the first
// value and therefore cannot see an appended duplicate. This test counts every
// value the server actually received.
func TestConformanceCallHeadersCannotAddASecondAuthorization(t *testing.T) {
	var got http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{}}`))
	}))
	t.Cleanup(srv.Close)

	client := newConformanceClient(t, srv.URL, "m")

	hostile := http.Header{}
	hostile.Set("Authorization", "Bearer attacker-chosen-token")
	hostile.Set("Content-Type", "text/plain")
	hostile.Set("Accept", "text/plain")

	_, err := client.Evaluate(context.Background(), "s", Questions{"q": NoulQuestion{Instructions: "i"}},
		WithCallHeaders(hostile))
	require.NoError(t, err)
	require.NotNil(t, got)

	// Assert all three authoritative headers before failing, so one run shows
	// the whole picture rather than stopping at the first one.
	auth := got.Values("Authorization")
	contentType := got.Values("Content-Type")
	accept := got.Values("Accept")

	assert.Lenf(t, auth, 1,
		"the client sent %d Authorization header values %q; a caller must not be able to append a second one", len(auth), auth)
	if len(auth) > 0 {
		assert.Equal(t, "Bearer conformance-test-key", auth[0])
	}

	assert.Lenf(t, contentType, 1, "the client sent %d Content-Type values %q", len(contentType), contentType)
	if len(contentType) > 0 {
		assert.Equal(t, "application/json", contentType[0])
	}

	assert.Lenf(t, accept, 1, "the client sent %d Accept values %q", len(accept), accept)
	if len(accept) > 0 {
		assert.Equal(t, "application/json", accept[0])
	}
}

// TestConformanceClientLevelHeaderCannotClobberAuthorization bounds the defect
// above: the *construction-time* path is safe, because New re-applies
// Authorization after copying caller headers. Only the per-call path is not.
func TestConformanceClientLevelHeaderCannotClobberAuthorization(t *testing.T) {
	var gotAuth []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Values("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{}}`))
	}))
	t.Cleanup(srv.Close)

	client, err := New(
		WithBaseURL(srv.URL),
		WithHeader("Authorization", "Bearer attacker-chosen-token"),
		WithAPIKey("conformance-test-key"),
		WithModel("m"),
		WithRetryPolicy(RetryPolicy{MaxRetries: 0}),
	)
	require.NoError(t, err)

	_, err = client.Evaluate(context.Background(), "s", Questions{"q": NoulQuestion{Instructions: "i"}})
	require.NoError(t, err)

	require.Len(t, gotAuth, 1, "WithHeader must be replaced by the real credential, not appended to it")
	assert.Equal(t, "Bearer conformance-test-key", gotAuth[0])
}

// TestConformanceCallHeadersStillPassThroughNonAuthoritativeHeaders makes sure
// the fix for the defect above cannot simply drop every caller header.
func TestConformanceCallHeadersStillPassThroughNonAuthoritativeHeaders(t *testing.T) {
	var gotTrace, gotTenant []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTrace = r.Header.Values("X-Trace-Id")
		gotTenant = r.Header.Values("X-Tenant")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{}}`))
	}))
	t.Cleanup(srv.Close)

	client := newConformanceClient(t, srv.URL, "m")

	extra := http.Header{}
	extra.Set("X-Trace-Id", "trace-9")
	extra.Set("X-Tenant", "acme")

	_, err := client.Evaluate(context.Background(), "s", Questions{"q": NoulQuestion{Instructions: "i"}},
		WithCallHeaders(extra))
	require.NoError(t, err)

	assert.Equal(t, []string{"trace-9"}, gotTrace)
	assert.Equal(t, []string{"acme"}, gotTenant)
}

// TestConformanceRetryResendsTheWholeBody guards the classic retry bug: a
// request body that is consumed by the first attempt and arrives empty on the
// second.
func TestConformanceRetryResendsTheWholeBody(t *testing.T) {
	var bodies []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := readAllForTest(r)
		if err != nil {
			t.Errorf("reading body: %v", err)
		}
		bodies = append(bodies, string(raw))

		if len(bodies) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"transient","code":500}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m","answers":{"q":{"type":"noul","noul":0.5}}}`))
	}))
	t.Cleanup(srv.Close)

	client, err := New(
		WithBaseURL(srv.URL),
		WithAPIKey("k"),
		WithModel("m"),
		WithRetryPolicy(RetryPolicy{
			MaxRetries: 5, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
			RetryStatuses: []int{500},
		}),
	)
	require.NoError(t, err)

	result, err := client.Evaluate(context.Background(), "the state", Questions{
		"q": NoulQuestion{Instructions: "the instructions", Criteria: &NoulCriteria{True: "t", False: "f"}},
	})
	require.NoError(t, err)
	got, ok := result.Noul("q")
	require.True(t, ok)
	assert.InDelta(t, 0.5, got.Noul, 0)

	require.Len(t, bodies, 3, "the request should have been attempted three times")
	for i, body := range bodies {
		assert.NotEmptyf(t, body, "attempt %d sent an empty body", i+1)
		assert.Containsf(t, body, "the state", "attempt %d lost the state", i+1)
		assert.Containsf(t, body, "the instructions", "attempt %d lost the questions", i+1)
		assert.Equalf(t, bodies[0], body, "attempt %d differed from the first", i+1)
	}
}

// TestConformanceRedirectIsNotFollowed pins the redirect policy.
//
// This endpoint is a single POST to a fixed URL, so a 3xx is not part of the
// contract. Go's default policy would follow it, and following it silently
// rewrites the request: 301/302/303 turn the POST into a GET, drop the body
// entirely, and replay the Authorization header at a target the server chose
// (see TestConformanceCustomHTTPClientOwnsRedirectPolicy, which still
// demonstrates exactly that when a caller injects their own client).
//
// This package therefore refuses to follow: the default client is built with
// CheckRedirect returning http.ErrUseLastResponse, so the 3xx comes back
// unchanged and Evaluate surfaces it as an *APIError. That is a deliberate
// decision, not an oversight — do not "fix" it back to the default policy.
func TestConformanceRedirectIsNotFollowed(t *testing.T) {
	var movedHits atomic.Int32
	var movedAuth atomic.Value
	movedAuth.Store("")

	mux := http.NewServeMux()
	mux.HandleFunc("/systemone", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/moved", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/moved", func(w http.ResponseWriter, r *http.Request) {
		movedHits.Add(1)
		movedAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{}}`))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := newConformanceClient(t, srv.URL, "m")
	result, err := client.Evaluate(context.Background(), "the state", Questions{"q": NoulQuestion{Instructions: "i"}})

	require.Error(t, err, "a 3xx is not part of the contract and must not be silently resolved")
	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrAPI)

	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusMovedPermanently, apiErr.StatusCode,
		"the un-followed 3xx must be surfaced with its real status")

	// The redirect target must never have been contacted at all.
	assert.Zerof(t, movedHits.Load(),
		"the redirect target was contacted %d time(s); the server must not be able to move the request", movedHits.Load())

	// And therefore the credential never reached it.
	assert.Emptyf(t, movedAuth.Load().(string),
		"the Authorization header reached the redirect target: %q", movedAuth.Load())
}

// TestConformanceCustomHTTPClientOwnsRedirectPolicy records the boundary of the
// hardening above: it lives on the client this package constructs, so supplying
// your own WithHTTPClient means you also own its redirect policy.
//
// This is not a defect — a caller who injects a client may have reasons for its
// policy — but it is worth knowing, because the built-in default is what keeps
// the credential on the configured host.
func TestConformanceCustomHTTPClientOwnsRedirectPolicy(t *testing.T) {
	var mu sync.Mutex
	var finalMethod, finalBody, finalAuth string

	mux := http.NewServeMux()
	mux.HandleFunc("/systemone", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/moved", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/moved", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := readAllForTest(r)
		mu.Lock()
		finalMethod, finalBody, finalAuth = r.Method, string(raw), r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{}}`))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, err := New(
		WithBaseURL(srv.URL),
		WithAPIKey("conformance-test-key"),
		WithModel("m"),
		WithHTTPClient(&http.Client{}), // the caller's client, default redirect policy
		WithRetryPolicy(RetryPolicy{MaxRetries: 0}),
	)
	require.NoError(t, err)

	_, err = client.Evaluate(context.Background(), "the state", Questions{"q": NoulQuestion{Instructions: "i"}})
	require.NoError(t, err, "with a caller-supplied client, Go's default policy follows the redirect")

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, http.MethodGet, finalMethod, "the POST was rewritten to a GET by the redirect")
	assert.Empty(t, finalBody, "the request body did not survive the redirect")
	assert.Equal(t, "Bearer conformance-test-key", finalAuth, "the credential was replayed at the redirect target")
	t.Logf("custom client followed the redirect: method=%s body_len=%d auth_present=%v",
		finalMethod, len(finalBody), finalAuth != "")
}

// TestConformanceCallHeadersDoNotLeakBetweenCalls guards the shared-state bug
// that would appear if Evaluate merged caller headers into the client's own
// http.Header instead of a clone.
func TestConformanceCallHeadersDoNotLeakBetweenCalls(t *testing.T) {
	var mu sync.Mutex
	seen := map[string][]string{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			State string `json:"state"`
		}
		raw, _ := readAllForTest(r)
		_ = json.Unmarshal(raw, &payload)

		mu.Lock()
		seen[payload.State] = r.Header.Values("X-Tenant")
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{}}`))
	}))
	t.Cleanup(srv.Close)

	client := newConformanceClient(t, srv.URL, "m")

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 50; i++ {
		withTenant := i%2 == 0
		state := fmt.Sprintf("state-%d", i)

		wg.Add(1)
		go func(state string, withTenant bool) {
			defer wg.Done()
			<-start
			var opts []CallOption
			if withTenant {
				h := http.Header{}
				h.Set("X-Tenant", "acme")
				opts = append(opts, WithCallHeaders(h))
			}
			_, err := client.Evaluate(context.Background(), state, Questions{"q": NoulQuestion{Instructions: "i"}}, opts...)
			if err != nil {
				t.Errorf("state %s: %v", state, err)
			}
		}(state, withTenant)
	}
	close(start)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, seen, 50)
	for i := 0; i < 50; i++ {
		state := fmt.Sprintf("state-%d", i)
		if i%2 == 0 {
			assert.Equalf(t, []string{"acme"}, seen[state], "%s should carry the caller header", state)
		} else {
			assert.Emptyf(t, seen[state], "%s must not inherit another call's header", state)
		}
	}
}

// TestConformanceUsageCostZeroIsNotAbsent pins the pointer semantics: a host
// that reports a cost of zero must be distinguishable from one that reports no
// cost at all.
func TestConformanceUsageCostZeroIsNotAbsent(t *testing.T) {
	cases := []struct {
		name     string
		usage    string
		wantNil  bool
		wantCost float64
	}{
		{name: "cost reported as zero", usage: `{"input_tokens":1,"output_tokens":2,"cost":0}`, wantNil: false, wantCost: 0},
		{name: "cost reported", usage: `{"input_tokens":1,"output_tokens":2,"cost":1.5e-05}`, wantNil: false, wantCost: 1.5e-05},
		{name: "cost absent", usage: `{"input_tokens":1,"output_tokens":2}`, wantNil: true},
		{name: "cost explicitly null", usage: `{"input_tokens":1,"output_tokens":2,"cost":null}`, wantNil: true},
		{name: "usage absent", usage: ``, wantNil: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"model":"m","answers":{}}`
			if tc.usage != "" {
				body = fmt.Sprintf(`{"model":"m","usage":%s,"answers":{}}`, tc.usage)
			}

			srv := newConformanceReplayServer(t, 200, []byte(body))
			client := newConformanceClient(t, srv.URL, "m")

			result, err := client.Evaluate(context.Background(), "s", Questions{"q": NoulQuestion{Instructions: "i"}})
			require.NoError(t, err)

			if tc.wantNil {
				assert.Nil(t, result.Usage.Cost, "an unreported cost must be a nil pointer, not zero")
				return
			}
			require.NotNil(t, result.Usage.Cost, "a reported cost must be a non-nil pointer")
			assert.InDelta(t, tc.wantCost, *result.Usage.Cost, 0)
		})
	}
}

// TestConformanceResponseBodySizeIsBounded pins MaxResponseBytes.
//
// The padded body is valid JSON (a JSON document may be followed by whitespace),
// so if nothing bounded the read it would decode successfully. That is what
// makes this a real test of the bound rather than a test that any large body
// happens to fail.
func TestConformanceResponseBodySizeIsBounded(t *testing.T) {
	base := []byte(`{"answers":{}}`)
	atLimit := make([]byte, 0, MaxResponseBytes)
	atLimit = append(atLimit, base...)
	atLimit = append(atLimit, bytes.Repeat([]byte(" "), MaxResponseBytes-len(base))...)

	require.Len(t, atLimit, MaxResponseBytes)
	require.True(t, json.Valid(atLimit), "the padded body must still be valid JSON, or this proves nothing")

	cases := []struct {
		name    string
		extra   int
		wantErr bool
	}{
		{name: "exactly at MaxResponseBytes", extra: 0, wantErr: false},
		{name: "one byte past MaxResponseBytes", extra: 1, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(atLimit)
				if tc.extra > 0 {
					_, _ = w.Write(bytes.Repeat([]byte(" "), tc.extra))
				}
			}))
			t.Cleanup(srv.Close)

			// Retries on: an oversized body must not be retried, so this also
			// proves the transport treats the size as a property of the host.
			client, err := New(
				WithBaseURL(srv.URL),
				WithAPIKey("k"),
				WithModel("m"),
				WithRetryPolicy(RetryPolicy{
					MaxRetries: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
					RetryConnectionErrors: true, RetryTimeoutErrors: true,
				}),
			)
			require.NoError(t, err)

			start := time.Now()
			result, err := client.Evaluate(context.Background(), "s", Questions{"q": NoulQuestion{Instructions: "i"}})
			elapsed := time.Since(start)

			if !tc.wantErr {
				require.NoError(t, err, "a body exactly at the limit must still be read")
				require.NotNil(t, result)
				return
			}

			require.Error(t, err, "a body past MaxResponseBytes must be an error, not an unbounded read")
			assert.ErrorIs(t, err, ErrDecode)
			assert.Nil(t, result)
			assert.Lessf(t, elapsed, 15*time.Second, "the bound must fail promptly rather than stream forever (took %s)", elapsed)
			assert.EqualValuesf(t, 1, hits.Load(),
				"an oversized body is a property of the host; retrying would buffer another %d bytes before failing again", MaxResponseBytes)
		})
	}
}

// TestConformanceOversizedBodyIsNotAConnectionError checks the sentinel
// contract stated in errors.go: "Every error this package returns wraps exactly
// one of these (or is a *APIError, which matches ErrAPI)."
//
// A response that is too large is not a connection failure: the connection
// worked, the host answered, and the answer was too big to buffer. A caller
// that branches on errors.Is(err, ErrConnection) to decide "the network is
// down, try later" must not be told that here.
//
// This uses the transport's unexported override so the test stays cheap; the
// boundary itself is covered at the real 32 MiB limit above.
func TestConformanceOversizedBodyIsNotAConnectionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"` + strings.Repeat("m", 4096) + `","answers":{}}`))
	}))
	t.Cleanup(srv.Close)

	client := newConformanceClient(t, srv.URL, "m")
	client.transport.maxResponseBytes = 1024

	_, err := client.Evaluate(context.Background(), "s", Questions{"q": NoulQuestion{Instructions: "i"}})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrDecode, "the oversized body must be reported as an unreadable response")

	assert.Falsef(t, errors.Is(err, ErrConnection),
		"an oversized body matched ErrConnection as well as ErrDecode, so the error wraps two sentinels; errors.go documents exactly one: %v", err)
	assert.Falsef(t, errors.Is(err, ErrTimeout), "an oversized body is not a timeout: %v", err)
	assert.Falsef(t, errors.Is(err, ErrAborted), "an oversized body is not caller cancellation: %v", err)
}

// TestConformancePartialNoulCriteriaShape pins what the client puts on the wire
// for a NoulCriteria that is only half filled in.
//
// The shape matters because the verified host rejects it: with criteria present,
// both "true" and "false" are required and neither may be null, so every shape
// below is a live HTTP 400. See
// TestConformanceNoulCriteriaWithNilSideIsRejectedLocally for the consequence.
func TestConformancePartialNoulCriteriaShape(t *testing.T) {
	cases := []struct {
		name     string
		criteria *NoulCriteria
		want     string
	}{
		{name: "zero value", criteria: &NoulCriteria{}, want: `{"true":null,"false":null}`},
		{name: "only the true side", criteria: &NoulCriteria{True: "yes"}, want: `{"true":"yes","false":null}`},
		{name: "only the false side", criteria: &NoulCriteria{False: "no"}, want: `{"true":null,"false":"no"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(NoulQuestion{Instructions: "i", Criteria: tc.criteria})
			require.NoError(t, err)

			var got struct {
				Criteria json.RawMessage `json:"criteria"`
			}
			require.NoError(t, json.Unmarshal(encoded, &got))
			assert.JSONEq(t, tc.want, string(got.Criteria))
		})
	}
}

// TestConformanceNoulCriteriaWithNilSideIsRejectedLocally checks that the client
// refuses to build a request the verified host answers with HTTP 400.
//
// Evidence from the live endpoint (raw output in the task-4 report and
// testdata/golden/PROBES.md). A noul question whose criteria object is present
// must carry BOTH sides, and neither may be null:
//
//	{"true":"y","false":"n"}   -> 200
//	{"true":"y","false":null}  -> 400  path ["questions","q","criteria","false"]
//	{"true":null,"false":"n"}  -> 400  path ["questions","q","criteria","true"]
//	{"true":null,"false":null} -> 400
//	{"true":"y"}               -> 400  (criteria.false is required)
//	criteria omitted entirely  -> 200
//
// NoulCriteria.True and .False are plain Entry fields without omitempty, so the
// only shapes the client can emit for a non-nil Criteria are the three pinned
// above -- and all three are 400s. ValidateQuestions currently declines to look
// at noul criteria at all ("a noul has nothing to validate"), so the mistake is
// sent to the server and comes back as an opaque zod message.
//
// The fix this test asks for is to reject a non-nil Criteria with a nil side
// before any network call, the same way choice and score criteria are checked --
// consistent with the deliberate decision to be stricter than a host that
// accepts less than the reference documents. If the ruling instead is that the
// official host accepts null here and this gateway is the outlier, delete this
// test and document the divergence in doc.go.
func TestConformanceNoulCriteriaWithNilSideIsRejectedLocally(t *testing.T) {
	cases := []struct {
		name     string
		criteria *NoulCriteria
	}{
		{name: "zero value", criteria: &NoulCriteria{}},
		{name: "only the true side", criteria: &NoulCriteria{True: "yes"}},
		{name: "only the false side", criteria: &NoulCriteria{False: "no"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newConformanceReplayServer(t, 200, []byte(`{"answers":{}}`))
			client := newConformanceClient(t, srv.URL, "m")

			_, err := client.Evaluate(context.Background(), "state", Questions{
				"q": NoulQuestion{Instructions: "i", Criteria: tc.criteria},
			})

			require.Errorf(t, err,
				"a NoulCriteria with a nil side is a live HTTP 400; it must be caught by validation, not sent")
			assert.ErrorIs(t, err, ErrValidation)
			assert.Zero(t, srv.hits(), "the request must never reach the network")
		})
	}
}

// TestConformanceNoulCriteriaOmittedEntirelyIsStillFine is the control for the
// test above: omitting criteria altogether is valid on the host, so the fix must
// not turn "no criteria" into an error.
func TestConformanceNoulCriteriaOmittedEntirelyIsStillFine(t *testing.T) {
	require.NoError(t, ValidateQuestions(Questions{
		"q": NoulQuestion{Instructions: "i"}, // Criteria nil -> key omitted
	}))

	// A fully specified criteria stays valid too.
	require.NoError(t, ValidateQuestions(Questions{
		"q": NoulQuestion{Instructions: "i", Criteria: &NoulCriteria{True: "y", False: "n"}},
	}))
}

// TestConformanceInvalidUTF8InStateIsReplaced records what happens to a Go
// string that is not valid UTF-8. encoding/json substitutes U+FFFD, so the state
// the model sees is not byte-identical to the state the caller passed. This is
// inherent to encoding/json rather than a bug in this package, but it is worth
// pinning: a caller slicing bytes out of a binary payload can silently lose
// them.
//
// The assertion is on the decoded value rather than on the bytes, because how
// encoding/json *spells* U+FFFD is not stable across Go releases: 1.23 writes the
// \ufffd escape, and later versions write the rune itself. Both decode to the
// same string, and the claim worth pinning is the substitution, not its
// spelling. Asserting bytes here made this test fail on Go 1.23 and pass on
// stable, which said nothing about the package.
func TestConformanceInvalidUTF8InStateIsReplaced(t *testing.T) {
	srv := newConformanceReplayServer(t, 200, []byte(`{"answers":{}}`))
	client := newConformanceClient(t, srv.URL, "m")

	invalid := "before\xff\xfe after"
	_, err := client.Evaluate(context.Background(), invalid, Questions{"q": NoulQuestion{Instructions: "i"}})
	require.NoError(t, err)

	body := srv.capturedBody()
	assert.NotContains(t, string(body), "\xff", "invalid UTF-8 must not reach the wire as-is")

	var sent struct {
		State string `json:"state"`
	}
	require.NoError(t, json.Unmarshal(body, &sent))
	assert.Equal(t, "before\uFFFD\uFFFD after", sent.State,
		"encoding/json replaces each invalid byte with U+FFFD; the caller cannot tell from the response")
}
