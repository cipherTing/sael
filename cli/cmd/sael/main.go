// Command sael measures a request's content against a moderation question set.
//
// It prints measurements, not decisions. Turning a probability into an action is
// the caller's job, because the threshold that is right for one relay is wrong
// for another.
package main

import (
	"os"

	"github.com/cipherTing/sael/cli/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
