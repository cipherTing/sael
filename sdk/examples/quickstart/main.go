// Command quickstart runs one classification through the sdk client.
//
// It reads ~/.Sael/config.json and ~/.Sael/auth.json, so it needs no flags and
// no environment variables once those exist:
//
//	mkdir -p ~/.Sael
//	echo '{"api_key":"..."}' > ~/.Sael/auth.json
//	chmod 600 ~/.Sael/auth.json
//	go run ./examples/quickstart "text to classify"
//
// The text defaults to a harmless sentence, so running it with no arguments is a
// connectivity check rather than a surprise.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/cipherTing/sael/sdk"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "quickstart:", err)
		os.Exit(1)
	}
}

func run() error {
	state := "Can you give me a good recipe for banana bread?"
	if len(os.Args) > 1 {
		state = os.Args[1]
	}

	// An empty directory means DefaultConfigDir: $SAEL_HOME, else ~/.Sael.
	client, err := sdk.NewFromConfigDir("")
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	started := time.Now()
	result, err := client.Evaluate(ctx, state, sdk.Questions{
		// Ask what you want to know about the request, one judgement per
		// question. Several narrow questions cost barely more than one, because
		// they are evaluated in parallel against the same state.
		"harmful_request": sdk.NoulQuestion{
			Instructions: "Does this ask for help harming people, or for help breaking the law?",
			Criteria: &sdk.NoulCriteria{
				True:  "It seeks assistance with harm or illegal activity.",
				False: "It does not.",
			},
		},
		"severity": sdk.ScoreQuestion{
			Instructions: "If this request were complied with, how much harm would it do?",
			Criteria: []sdk.Entry{
				"None: an ordinary request.",
				"Mild: sensitive, but complying does no real damage.",
				"Serious: complying enables real wrongdoing.",
				"Severe: complying causes serious harm.",
			},
		},
	})
	if err != nil {
		return describe(err)
	}

	harmful, ok := result.Noul("harmful_request")
	if !ok {
		return errors.New("the response carried no answer named harmful_request")
	}
	severity, ok := result.Score("severity")
	if !ok {
		return errors.New("the response carried no answer named severity")
	}

	fmt.Printf("model     : %s\n", result.Model)
	fmt.Printf("latency   : %s\n", time.Since(started).Round(time.Millisecond))
	fmt.Printf("input     : %d tokens\n", result.Usage.InputTokens)
	if result.Usage.Cost != nil {
		fmt.Printf("cost      : $%.8f\n", *result.Usage.Cost)
	}
	fmt.Printf("harmful   : %.3f\n", harmful.Noul)
	fmt.Printf("severity  : %.2f of %d levels\n", severity.Score, len(severity.Legend))
	for level, label := range severity.Legend {
		fmt.Printf("    %d %-60s %.2f\n", level, label, severity.Probabilities[level])
	}
	return nil
}

// describe turns a client error into something worth reading, which is most of
// the value of having distinct sentinels.
func describe(err error) error {
	var rate *sdk.RateLimitError
	var api *sdk.APIError

	switch {
	case errors.As(err, &rate):
		return fmt.Errorf("rate limited; the host asked for a %s wait: %w", rate.RetryAfter, err)
	case errors.As(err, &api):
		return fmt.Errorf("the host rejected the request with HTTP %d: %s", api.StatusCode, api.Message)
	case errors.Is(err, sdk.ErrValidation):
		return fmt.Errorf("the question set is not valid, so nothing was sent: %w", err)
	case errors.Is(err, sdk.ErrConfig):
		return fmt.Errorf("the configuration is unusable: %w", err)
	case errors.Is(err, sdk.ErrAborted), errors.Is(err, sdk.ErrTimeout):
		return fmt.Errorf("gave up before the host answered: %w", err)
	case errors.Is(err, sdk.ErrConnection):
		return fmt.Errorf("could not reach the host: %w", err)
	default:
		return err
	}
}
