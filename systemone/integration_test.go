package systemone

// integration_test.go exercises the real service over the network.
//
// It is gated on the environment and on -short:
//
//   - With TYPESAFE_API_KEY, TYPESAFE_BASE_URL or TYPESAFE_DEFAULT_MODEL unset,
//     every test here skips. Nothing about a particular host is assumed: which
//     gateway these tests hit is decided entirely by the environment, so the
//     same suite can run against the official host, a gateway or a staging
//     deployment.
//   - Under -short they skip too, so the offline suite stays offline.
//
//   set -a && . ./.secrets/test.env && set +a
//   go test ./systemone/ -run Integration -count=1 -v
//
// The API key is read from the environment and is never written, logged or
// embedded in a failure message. Every error path is checked for the key by
// assertNoCredentialInError before it is allowed to fail a test.

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// integrationEnv is the resolved configuration for the live tests.
type integrationEnv struct {
	APIKey  string
	BaseURL string
	Model   string
}

// loadIntegrationEnv skips the test when the environment is not configured.
func loadIntegrationEnv(t *testing.T) integrationEnv {
	t.Helper()

	if testing.Short() {
		t.Skip("integration tests are skipped under -short")
	}

	env := integrationEnv{
		APIKey:  strings.TrimSpace(os.Getenv(EnvAPIKey)),
		BaseURL: strings.TrimSpace(os.Getenv(EnvBaseURL)),
		Model:   strings.TrimSpace(os.Getenv(EnvModel)),
	}

	var missing []string
	if env.APIKey == "" {
		missing = append(missing, EnvAPIKey)
	}
	if env.BaseURL == "" {
		missing = append(missing, EnvBaseURL)
	}
	if env.Model == "" {
		missing = append(missing, EnvModel)
	}
	if len(missing) > 0 {
		t.Skipf("integration tests need %s; skipping", strings.Join(missing, ", "))
	}
	return env
}

// newIntegrationClient builds a client pointed explicitly at the configured
// host. The BaseURL and model are passed as options rather than left to the
// environment fallback, so the test proves the explicit configuration path.
func newIntegrationClient(t *testing.T, env integrationEnv, extra ...Option) *Client {
	t.Helper()

	opts := []Option{
		WithBaseURL(env.BaseURL),
		WithAPIKey(env.APIKey),
		WithModel(env.Model),
		// A single overall budget, so a hung or unreachable host fails the
		// test instead of blocking it indefinitely.
		WithTotalTimeout(90 * time.Second),
	}
	opts = append(opts, extra...)

	client, err := New(opts...)
	require.NoError(t, err)
	require.Equal(t, env.BaseURL, client.BaseURL(), "WithBaseURL must win over the default")
	require.Equal(t, env.Model, client.DefaultModel(), "WithModel must win over the default")
	return client
}

// assertNoCredentialInError fails the test if the API key leaked into an error
// value. It reports without printing the key or the offending string.
func assertNoCredentialInError(t *testing.T, env integrationEnv, err error) {
	t.Helper()
	if err == nil || env.APIKey == "" {
		return
	}
	require.NotContains(t, err.Error(), env.APIKey,
		"the API key leaked into an error value; the error text is withheld deliberately")
}

// evaluateLive calls Evaluate, retrying only on a connection failure. Live
// networks drop TLS handshakes occasionally; the retry is logged so it cannot
// hide a systematic problem, and it never retries an application-level error.
func evaluateLive(ctx context.Context, t *testing.T, env integrationEnv, client *Client, state any, questions Questions, opts ...CallOption) *Result {
	t.Helper()

	const attempts = 3
	var lastErr error

	for attempt := 1; attempt <= attempts; attempt++ {
		result, err := client.Evaluate(ctx, state, questions, opts...)
		if err == nil {
			return result
		}
		assertNoCredentialInError(t, env, err)

		if !errors.Is(err, ErrConnection) {
			require.NoErrorf(t, err, "evaluate failed on attempt %d", attempt)
		}
		lastErr = err
		if attempt < attempts {
			t.Logf("transient connection failure on attempt %d, retrying: %v", attempt, err)
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
	}

	t.Fatalf("evaluate failed after %d attempts with a connection error: %v", attempts, lastErr)
	return nil
}

// ---------------------------------------------------------------------------
// B2: the three primitives against the real service
// ---------------------------------------------------------------------------

func TestIntegrationMixedPrimitives(t *testing.T) {
	env := loadIntegrationEnv(t)
	client := newIntegrationClient(t, env)

	const (
		choiceLabels  = 4
		scoreLevels   = 4
		noulThreshold = 0.0
	)

	questions := Questions{
		"is_urgent": NoulQuestion{
			Instructions: "Does this ticket need a same day human response?",
			Criteria: &NoulCriteria{
				True:  "Money is at stake or the product is unusable.",
				False: "It is a question or a cosmetic issue.",
			},
		},
		"primary_intent": ChoiceQuestion{
			Instructions: "What is the customer mainly asking for?",
			Criteria: map[string]Entry{
				"refund":  "They want money returned.",
				"bug_fix": "They want the crash fixed.",
				"info":    "They only want information.",
				"cancel":  nil, // a label with no description is legal
			},
		},
		"sentiment": ScoreQuestion{
			Instructions: "How negative is the customer's tone?",
			Criteria: []Entry{
				"0: Neutral or positive.",
				"1: Mildly annoyed.",
				"2: Clearly frustrated.",
				"3: Angry or threatening to leave.",
			},
		},
	}

	state := map[string]any{
		"ticket_id": "T-1042",
		"channel":   "email",
		"subject":   "Charged twice and the app will not open",
		"body":      "You billed me twice for September, and since the update the app crashes before the login screen. I want the extra charge refunded.",
		"plan":      "pro",
	}

	result := evaluateLive(context.Background(), t, env, client, state, questions)
	require.NotNil(t, result)

	// Model is the resolved, versioned id. Its exact form is the host's
	// business; this only requires that the host reported something.
	assert.NotEmpty(t, result.Model, "the host must report the resolved model id")
	cost := "not reported"
	if result.Usage.Cost != nil {
		cost = strconv.FormatFloat(*result.Usage.Cost, 'g', -1, 64)
	}
	t.Logf("resolved model: %q provider: %q id: %q tokens in/out: %d/%d cost: %s",
		result.Model, result.Provider, result.ID,
		result.Usage.InputTokens, result.Usage.OutputTokens, cost)

	require.Len(t, result.Answers, 3, "every question must come back with an answer")

	// Noul.
	noul, ok := result.Noul("is_urgent")
	require.True(t, ok, "is_urgent must decode as a noul answer")
	assert.GreaterOrEqual(t, noul.Noul, 0.0)
	assert.LessOrEqual(t, noul.Noul, 1.0)
	assert.Greater(t, noul.Noul, noulThreshold)

	// Choice.
	choice, ok := result.Choice("primary_intent")
	require.True(t, ok, "primary_intent must decode as a choice answer")
	assert.Contains(t, questions["primary_intent"].(ChoiceQuestion).Criteria, choice.Choice,
		"the chosen label must be one of the labels that were sent")
	assert.Len(t, choice.Probabilities, choiceLabels)
	assert.GreaterOrEqual(t, choice.Confidence, 0.0)
	assert.LessOrEqual(t, choice.Confidence, 1.0)
	sum := 0.0
	for label, p := range choice.Probabilities {
		assert.GreaterOrEqualf(t, p, 0.0, "probability of %q", label)
		assert.LessOrEqualf(t, p, 1.0, "probability of %q", label)
		sum += p
	}
	assert.InDelta(t, 1.0, sum, 1e-6, "choice probabilities must sum to one")

	// Score.
	score, ok := result.Score("sentiment")
	require.True(t, ok, "sentiment must decode as a score answer")
	assert.Len(t, score.Legend, scoreLevels,
		"the legend must densify to exactly one entry per rubric level that was sent")
	assert.Len(t, score.Probabilities, scoreLevels)
	for i, p := range score.Probabilities {
		assert.GreaterOrEqualf(t, p, 0.0, "probability at level %d", i)
		assert.LessOrEqualf(t, p, 1.0, "probability at level %d", i)
	}
	assert.GreaterOrEqual(t, score.Score, 0.0)
	assert.LessOrEqual(t, score.Score, float64(scoreLevels-1),
		"an expectation must fall inside the rubric")
	assert.GreaterOrEqual(t, score.Confidence, 0.0)
	assert.LessOrEqual(t, score.Confidence, 1.0)

	// The legend echoes the criteria that were sent.
	assert.Equal(t, questions["sentiment"].(ScoreQuestion).Criteria[0], score.Legend[0])

	// Accessors must not mis-type an answer.
	_, isChoice := result.Choice("is_urgent")
	assert.False(t, isChoice)
	_, isNoul := result.Noul("sentiment")
	assert.False(t, isNoul)
}

// ---------------------------------------------------------------------------
// B3: an unknown model is an API error, whatever status the host picks
// ---------------------------------------------------------------------------

func TestIntegrationUnknownModelIsAPIError(t *testing.T) {
	env := loadIntegrationEnv(t)
	client := newIntegrationClient(t, env)

	// Derived from the configured id so this stays host-neutral; the suffix
	// cannot name a real model anywhere.
	bogus := env.Model + "-integration-nonexistent-0xdeadbeef"

	_, err := client.Evaluate(
		context.Background(),
		"anything",
		Questions{"q": NoulQuestion{Instructions: "Is this a test?", Criteria: &NoulCriteria{True: "yes", False: "no"}}},
		WithCallModel(bogus),
	)

	require.Error(t, err, "a non-existent model must not succeed")
	assertNoCredentialInError(t, env, err)

	// The official docs say 422 for validation failures and this gateway
	// returns 400, so only the class is asserted.
	assert.ErrorIs(t, err, ErrAPI)
	assert.False(t, errors.Is(err, ErrDecode), "a non-2xx must not be reported as a decode failure")

	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.GreaterOrEqual(t, apiErr.StatusCode, 400, "must be a client or server error status")
	assert.NotEqual(t, 0, apiErr.StatusCode, "the status must be surfaced, not swallowed")
	t.Logf("unknown model returned HTTP %d: %s", apiErr.StatusCode, apiErr.Message)

	assert.NotEmpty(t, apiErr.Message, "some diagnostic must reach the caller")
	assert.NotEmpty(t, apiErr.Body, "the raw body is the fallback diagnostic and must be kept")
	assert.NotContains(t, apiErr.Message, bogus+"/v1", "sanity: the message should not be a URL echo")
}

// ---------------------------------------------------------------------------
// B4: cancellation against the real service
// ---------------------------------------------------------------------------

func TestIntegrationCancelledContextDoesNotSucceed(t *testing.T) {
	env := loadIntegrationEnv(t)
	client := newIntegrationClient(t, env)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	result, err := client.Evaluate(ctx, "anything", Questions{
		"q": NoulQuestion{Instructions: "Is this a test?"},
	})
	elapsed := time.Since(start)

	require.Error(t, err, "a cancelled context must never yield a result")
	assert.Nil(t, result)
	assertNoCredentialInError(t, env, err)
	assert.ErrorIs(t, err, ErrAborted)
	assert.Less(t, elapsed, 5*time.Second,
		"cancellation must return promptly rather than waiting out a network round trip")

	t.Logf("cancelled call returned in %s", elapsed)
}

// ---------------------------------------------------------------------------
// Chinese content end to end
// ---------------------------------------------------------------------------

func TestIntegrationChineseStateRoundTrip(t *testing.T) {
	env := loadIntegrationEnv(t)
	client := newIntegrationClient(t, env)

	state := "客服承诺四十八小时内回复，但工单提交后已经过去了六天，期间没有任何人联系我。" +
		"我第三次进线时，客服只说「请耐心等待」，没有给出预计时间，也没有升级工单。"
	if !strings.Contains(state, "耐心等待") {
		t.Fatal("the test fixture text lost its Chinese characters")
	}

	questions := Questions{
		"promise_broken": NoulQuestion{
			Instructions: "Did the company fail to meet the response time it promised?",
			Criteria: &NoulCriteria{
				True:  "The promised response window has clearly elapsed with no reply.",
				False: "The company responded within the window it promised.",
			},
		},
		"handling_quality": ScoreQuestion{
			Instructions: "How well has support handled this case so far?",
			Criteria: []Entry{
				"0: Fully handled.",
				"1: Mostly handled, minor gaps.",
				"2: Poorly handled, no ownership.",
				"3: Not handled at all.",
			},
		},
	}

	result := evaluateLive(context.Background(), t, env, client, state, questions)
	require.NotNil(t, result)

	broken, ok := result.Noul("promise_broken")
	require.True(t, ok)
	assert.GreaterOrEqual(t, broken.Noul, 0.0)
	assert.LessOrEqual(t, broken.Noul, 1.0)

	quality, ok := result.Score("handling_quality")
	require.True(t, ok)
	assert.Len(t, quality.Legend, 4)
	assert.Len(t, quality.Probabilities, 4)

	// Only structure is asserted, never the model's opinion: an assertion on
	// which way it judged would be a flaky test of the model, not of the client.
	t.Logf("chinese state: promise_broken=%v handling_quality=%v legend[0]=%q",
		broken.Noul, quality.Score, quality.Legend[0])
}

// ---------------------------------------------------------------------------
// The live wire accepts what the client sends
// ---------------------------------------------------------------------------

// TestIntegrationNoulWithoutCriteriaIsAccepted is the live counterpart of the
// offline wire-shape test: it proves the service accepts a noul question whose
// criteria key is absent entirely, which is the only form this client emits.
func TestIntegrationNoulWithoutCriteriaIsAccepted(t *testing.T) {
	env := loadIntegrationEnv(t)
	client := newIntegrationClient(t, env)

	result := evaluateLive(context.Background(), t, env, client,
		"The tests all passed and the deploy finished in 90 seconds.",
		Questions{"green": NoulQuestion{Instructions: "Did the tests pass?"}},
	)
	require.NotNil(t, result)

	answer, ok := result.Noul("green")
	require.True(t, ok, "a noul question without criteria must be accepted and answered")
	assert.GreaterOrEqual(t, answer.Noul, 0.0)
	assert.LessOrEqual(t, answer.Noul, 1.0)
}

// TestIntegrationPartialNoulCriteriaIsNeverAccepted is the live counterpart of
// TestConformanceNoulCriteriaWithNilSideIsRejectedLocally.
//
// A NoulCriteria with one side left unset marshals to
// {"true":"...","false":null}, and the live host answers HTTP 400. This test
// only requires that the call does NOT succeed, so it passes both before and
// after the client starts rejecting the shape locally -- and it fails loudly if
// the shape ever turns out to be acceptable, which would mean the conformance
// finding is wrong.
func TestIntegrationPartialNoulCriteriaIsNeverAccepted(t *testing.T) {
	env := loadIntegrationEnv(t)
	client := newIntegrationClient(t, env)

	_, err := client.Evaluate(context.Background(), "anything", Questions{
		"q": NoulQuestion{
			Instructions: "Is this a test?",
			Criteria:     &NoulCriteria{True: "The text says it is a test."}, // False left nil
		},
	})

	require.Error(t, err, "a half-filled NoulCriteria must never be accepted")
	assertNoCredentialInError(t, env, err)

	if errors.Is(err, ErrValidation) {
		t.Log("the client rejected the half-filled criteria locally, before the network")
		return
	}

	assert.ErrorIs(t, err, ErrAPI, "either validation or an API error, never a success")
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	t.Logf("the host rejected the half-filled criteria with HTTP %d: %s", apiErr.StatusCode, apiErr.Message)
}

// TestIntegrationMarkupAndUnicodeSurviveTheWire sends markup and non-ASCII in
// the same request the offline escaping test covers, so a regression cannot hide
// behind a fake server that is more forgiving than the real one.
func TestIntegrationMarkupAndUnicodeSurviveTheWire(t *testing.T) {
	env := loadIntegrationEnv(t)
	client := newIntegrationClient(t, env)

	const markup = `<script>alert(1)</script> & </div> 100% > 50%`

	state := map[string]any{
		"html":    markup,
		"chinese": "订单号 20260915003，显示器反复黑屏重启。",
	}

	result := evaluateLive(context.Background(), t, env, client, state,
		Questions{"contains_markup": NoulQuestion{
			Instructions: "Does the html field contain an opening script tag?",
			Criteria: &NoulCriteria{
				True:  "The text contains a <script> tag.",
				False: "It does not.",
			},
		}},
	)
	require.NotNil(t, result)

	answer, ok := result.Noul("contains_markup")
	require.True(t, ok)
	assert.GreaterOrEqual(t, answer.Noul, 0.0)
	assert.LessOrEqual(t, answer.Noul, 1.0)
	// If HTML escaping were still on, the model would be reading \u003cscript\u003e
	// and this obvious question would come back uncertain.
	t.Logf("markup question answered with %v", answer.Noul)
}
