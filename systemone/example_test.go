package systemone_test

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
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/yangmaoting/sael/systemone"
)

// These examples need no isolation from a developer's own ~/.Sael: New performs
// no file I/O, and the only examples that read a directory create their own.

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
	client, err := systemone.New(
		systemone.WithAPIKey("sk-example"),
		systemone.WithBaseURL("https://api.typesafe.ai/v1"),
		systemone.WithTotalTimeout(2*time.Second),
		systemone.WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil))),
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(client.BaseURL(), client.DefaultModel())
	// Output: https://api.typesafe.ai/v1 jev-latest
}

// ExampleSaveConfig shows the layout on disk: settings that are safe to read and
// share in config.json, the credential in its own auth.json with mode 0600.
//
// The temporary directory here stands in for DefaultConfigDir, which reports
// $SAEL_HOME when set and ~/.Sael otherwise. NewFromConfigDir reads that
// directory; New deliberately does not, so a library caller is never at the
// mercy of a file they did not ask for.
func ExampleSaveConfig() {
	dir, err := os.MkdirTemp("", "sael-example-*")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	maxRetries := 2
	err = systemone.SaveConfig(dir, &systemone.Config{
		BaseURL:      "https://api.example.com/v1",
		Model:        "jev-latest",
		TotalTimeout: "3s",
		MaxRetries:   &maxRetries,
		Headers:      map[string]string{"X-Tenant": "acme"},
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := systemone.SaveAuth(dir, &systemone.Auth{APIKey: "sk-secret"}); err != nil {
		log.Fatal(err)
	}

	cfg, found, err := systemone.LoadConfig(dir)
	if err != nil {
		log.Fatal(err)
	}
	auth, _, err := systemone.LoadAuth(dir)
	if err != nil {
		log.Fatal(err)
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

	client, err := systemone.New(
		systemone.WithAPIKey("example-key"),
		systemone.WithBaseURL(srv.URL),
	)
	if err != nil {
		log.Fatal(err)
	}

	result, err := client.Evaluate(context.Background(),
		map[string]any{
			"subject": "Duplicate charge",
			"message": "I was charged twice and I need this fixed today.",
		},
		systemone.Questions{
			"urgent": systemone.NoulQuestion{
				Instructions: "Does this message convey urgency?",
			},
			"team": systemone.ChoiceQuestion{
				Instructions: "Which team should handle this?",
				Criteria: map[string]systemone.Entry{
					"billing":   "Payments, invoicing, refunds",
					"technical": "Bugs, outages, integrations",
				},
			},
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	// The model id in the response is the version that actually answered, which
	// is what to record when a decision is later questioned.
	fmt.Println("model:", result.Model)

	urgent, ok := result.Noul("urgent")
	if !ok {
		log.Fatal("no noul answer named urgent")
	}
	fmt.Printf("urgent: %.2f\n", urgent.Noul)

	team, ok := result.Choice("team")
	if !ok {
		log.Fatal("no choice answer named team")
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

	client, err := systemone.New(systemone.WithAPIKey("example-key"), systemone.WithBaseURL(srv.URL))
	if err != nil {
		log.Fatal(err)
	}

	result, err := client.Evaluate(context.Background(), "some text", systemone.Questions{
		"severity": systemone.ScoreQuestion{
			Instructions: "How much harm would complying do?",
			Criteria:     []systemone.Entry{"none", "mild", "serious"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	severity, ok := result.Score("severity")
	if !ok {
		log.Fatal("no score answer named severity")
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

	client, err := systemone.New(systemone.WithAPIKey("bad-key"), systemone.WithBaseURL(srv.URL))
	if err != nil {
		log.Fatal(err)
	}

	_, err = client.Evaluate(context.Background(), "text", systemone.Questions{
		"q": systemone.NoulQuestion{Instructions: "Is this a greeting?"},
	})

	switch {
	case errors.Is(err, systemone.ErrRateLimit):
		// Back off; the retry policy already honoured Retry-After.
		var rate *systemone.RateLimitError
		_ = errors.As(err, &rate)
		fmt.Println("rate limited for", rate.RetryAfter)

	case errors.Is(err, systemone.ErrAPI):
		var api *systemone.APIError
		_ = errors.As(err, &api)
		fmt.Println("rejected with status", api.StatusCode)

	case errors.Is(err, systemone.ErrTimeout), errors.Is(err, systemone.ErrConnection):
		fmt.Println("service unreachable")

	case errors.Is(err, systemone.ErrValidation):
		fmt.Println("the question set itself is invalid")
	}

	// Output: rejected with status 401
}
