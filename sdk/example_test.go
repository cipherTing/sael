package sdk_test

// These examples are the package's usage documentation. Go runs them as part of
// the test suite, so they cannot drift out of date the way a separate document
// would: the expected output below each one is asserted on every build.
//
// They live in the external test package to demonstrate the API exactly as a
// caller sees it, using only exported identifiers.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/cipherTing/sael/sdk"
)

// These examples need no isolation from a developer's own ~/.Sael: New performs
// no file I/O, and the only examples that read a directory create their own.
//
// The environment is a different matter. New consults it for anything an Option
// does not set, and an example's output is asserted, so a variable the developer
// happens to have exported would change the text the example expects. The
// examples that print an environment-derived value clear it first. This is not
// hypothetical: with TYPESAFE_DEFAULT_MODEL exported, ExampleNew failed.

// clearEnv removes an environment variable and returns a function that puts it
// back, so an example can write `defer clearEnv("NAME")()`.
//
// Examples have no *testing.T and therefore no t.Setenv, which is the whole
// reason this exists.
func clearEnv(name string) func() {
	value, existed := os.LookupEnv(name)
	_ = os.Unsetenv(name)

	return func() {
		if existed {
			_ = os.Setenv(name, value)
			return
		}
		_ = os.Unsetenv(name)
	}
}

// must panics on a non-nil error.
//
// Examples must not return early: a missing line of output is reported as a
// confusing mismatch rather than a clear failure. log.Fatal is the obvious
// choice and the wrong one, because it calls os.Exit and therefore skips
// deferred cleanup such as the test server's Close. Panicking runs the defers
// and fails the example with the error that caused it.
func must(err error) {
	if err != nil {
		panic(err)
	}
}

// fail panics on a broken assumption, for the cases where the failure is a
// missing answer rather than a returned error.
func fail(format string, args ...any) {
	panic(fmt.Errorf(format, args...))
}

// cannedServer stands in for the API so the examples run offline and
// deterministically. A real caller talks to the real endpoint.
func cannedServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "model": "jev-1.13.0",
		  "answers": {
		    "urgent": {"type": "noul", "noul": 0.97},
		    "team":   {"type": "choice", "choice": "billing",
		               "probabilities": {"billing": 0.9, "technical": 0.1}, "confidence": 0.8}
		  },
		  "usage": {"input_tokens": 312, "output_tokens": 48}
		}`))
	}))
}

// ExampleNew shows the smallest useful client: a key, a target, and a deadline.
//
// In real code the key comes from the environment through EnvAPIKey rather than
// being written into source; it is spelled out here so the example is
// self-contained.
//
// WithTotalTimeout is worth setting. The retry policy allows three attempts and
// the per-attempt timeout defaults to ten seconds, so without an overall budget a
// call can occupy a request path for far longer than the caller intended.
func ExampleNew() {
	// The default model is only jev-latest when nothing overrides it, so the
	// variable that would is cleared: an example's output has to be the same on
	// every machine.
	defer clearEnv("TYPESAFE_DEFAULT_MODEL")()

	client, err := sdk.New(
		sdk.WithAPIKey("sk-example"),
		sdk.WithBaseURL("https://api.typesafe.ai/v1"),
		sdk.WithTotalTimeout(2*time.Second),
		sdk.WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil))),
	)
	if err != nil {
		must(err)
	}
	fmt.Println(client.BaseURL(), client.DefaultModel())
	// Output: https://api.typesafe.ai/v1 jev-latest
}

// ExampleSaveConfig shows the layout on disk: settings that are safe to read and
// share in config.json, and the credential in its own auth.json so that the half
// people share is never the half that authenticates them.
//
// The temporary directory here stands in for DefaultConfigDir, which reports
// $SAEL_HOME when set and ~/.Sael otherwise. NewFromConfigDir reads that
// directory; New deliberately does not, so a library caller is never at the
// mercy of a file they did not ask for.
func ExampleSaveConfig() {
	dir, err := os.MkdirTemp("", "sael-example-*")
	if err != nil {
		must(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	maxRetries := 2
	err = sdk.SaveConfig(dir, &sdk.Config{
		BaseURL:      "https://api.example.com/v1",
		Model:        "jev-latest",
		TotalTimeout: "3s",
		MaxRetries:   &maxRetries,
		Headers:      map[string]string{"X-Tenant": "acme"},
	})
	if err != nil {
		must(err)
	}
	if err := sdk.SaveAuth(dir, &sdk.Auth{APIKey: "sk-secret"}); err != nil {
		must(err)
	}

	cfg, found, err := sdk.LoadConfig(dir)
	if err != nil {
		must(err)
	}
	auth, _, err := sdk.LoadAuth(dir)
	if err != nil {
		must(err)
	}

	fmt.Println("found:        ", found)
	fmt.Println("base_url:     ", cfg.BaseURL)
	fmt.Println("model:        ", cfg.Model)
	fmt.Println("total_timeout:", cfg.TotalTimeout)
	fmt.Println("max_retries:  ", *cfg.MaxRetries)
	fmt.Println("key loaded:   ", auth.APIKey != "")

	// Output:
	// found:         true
	// base_url:      https://api.example.com/v1
	// model:         jev-latest
	// total_timeout: 3s
	// max_retries:   2
	// key loaded:    true
}

// ExampleClient_Evaluate asks three kinds of question in one call and reads the
// typed answers back.
//
// One request carries every question, which is the point of the API: the
// questions are evaluated in parallel against the same state, so adding questions
// costs a few tokens rather than another round trip. Ask several narrow questions
// and combine them in your own code, where the thresholds belong.
func ExampleClient_Evaluate() {
	srv := cannedServer()
	defer srv.Close()

	client, err := sdk.New(
		sdk.WithAPIKey("example-key"),
		sdk.WithBaseURL(srv.URL),
	)
	if err != nil {
		must(err)
	}

	result, err := client.Evaluate(context.Background(),
		map[string]any{
			"subject": "Duplicate charge",
			"message": "I was charged twice and I need this fixed today.",
		},
		sdk.Questions{
			"urgent": sdk.NoulQuestion{
				Instructions: "Does this message convey urgency?",
			},
			"team": sdk.ChoiceQuestion{
				Instructions: "Which team should handle this?",
				Criteria: map[string]sdk.Entry{
					"billing":   "Payments, invoicing, refunds",
					"technical": "Bugs, outages, integrations",
				},
			},
		},
	)
	if err != nil {
		must(err)
	}

	// The model id in the response is the version that actually answered, which
	// is what to record when a decision is later questioned.
	fmt.Println("model:", result.Model)

	urgent, ok := result.Noul("urgent")
	if !ok {
		fail("no noul answer named urgent")
	}
	fmt.Printf("urgent: %.2f\n", urgent.Noul)

	team, ok := result.Choice("team")
	if !ok {
		fail("no choice answer named team")
	}
	fmt.Printf("team: %s (confidence %.1f)\n", team.Choice, team.Confidence)

	fmt.Println("input tokens:", result.Usage.InputTokens)

	// Output:
	// model: jev-1.13.0
	// urgent: 0.97
	// team: billing (confidence 0.8)
	// input tokens: 312
}

// ExampleResult_Score reads a Score answer, which reports an expectation across
// an ordered rubric rather than a single level.
//
// Score is fractional, so it can land between levels. Index Legend and
// Probabilities with the score to read the distribution around it.
func ExampleResult_Score() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "model": "jev-1.13.0",
		  "answers": {"severity": {
		    "type": "score", "score": 1.2, "confidence": 0.9,
		    "legend": {"0": "none", "1": "mild", "2": "serious"},
		    "probabilities": {"0": 0.1, "1": 0.6, "2": 0.3}
		  }},
		  "usage": {"input_tokens": 100, "output_tokens": 20}
		}`))
	}))
	defer srv.Close()

	client, err := sdk.New(sdk.WithAPIKey("example-key"), sdk.WithBaseURL(srv.URL))
	if err != nil {
		must(err)
	}

	result, err := client.Evaluate(context.Background(), "some text", sdk.Questions{
		"severity": sdk.ScoreQuestion{
			Instructions: "How much harm would complying do?",
			Criteria:     []sdk.Entry{"none", "mild", "serious"},
		},
	})
	if err != nil {
		must(err)
	}

	severity, ok := result.Score("severity")
	if !ok {
		fail("no score answer named severity")
	}
	for level, label := range severity.Legend {
		fmt.Printf("%d %-8s %.1f\n", level, label, severity.Probabilities[level])
	}
	// Output:
	// 0 none     0.1
	// 1 mild     0.6
	// 2 serious  0.3
}

// ExampleClient_Evaluate_errors shows the three failures a caller has to tell
// apart: a rejected request, a refused credential, and a service that is simply
// unreachable.
//
// Every error matches one of the package's sentinels, so a single errors.Is
// switch is enough to decide between failing the user's request, failing over to
// another backend, or failing open.
func ExampleClient_Evaluate_errors() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key","code":401}}`))
	}))
	defer srv.Close()

	client, err := sdk.New(sdk.WithAPIKey("bad-key"), sdk.WithBaseURL(srv.URL))
	if err != nil {
		must(err)
	}

	_, err = client.Evaluate(context.Background(), "text", sdk.Questions{
		"q": sdk.NoulQuestion{Instructions: "Is this a greeting?"},
	})

	switch {
	case errors.Is(err, sdk.ErrRateLimit):
		// Back off; the retry policy already honoured Retry-After.
		var rate *sdk.RateLimitError
		_ = errors.As(err, &rate)
		fmt.Println("rate limited for", rate.RetryAfter)

	case errors.Is(err, sdk.ErrAPI):
		var api *sdk.APIError
		_ = errors.As(err, &api)
		fmt.Println("rejected with status", api.StatusCode)

	case errors.Is(err, sdk.ErrTimeout), errors.Is(err, sdk.ErrConnection):
		fmt.Println("service unreachable")

	case errors.Is(err, sdk.ErrValidation):
		fmt.Println("the question set itself is invalid")
	}

	// Output: rejected with status 401
}
