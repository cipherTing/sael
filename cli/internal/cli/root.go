// Package cli holds sael's command tree.
package cli

import (
	"github.com/spf13/cobra"
)

// Execute runs sael. It returns an error when the command failed, so main can
// exit non-zero; the error has already been reported to the user by then.
func Execute() error {
	return newRootCmd().Execute()
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "sael",
		Short: "Measure a request's content against a moderation question set",
		Long: `sael sends a request to the Jev model together with a fixed set of moderation
questions, and prints what came back.

It reports measurements, not decisions. Each answer is a probability or a
position on a scale, and what counts as "too much" depends on the relay that is
asking, so that judgement is left to the caller.`,
		// The usage block is noise next to a real error; a wrong flag is already
		// reported clearly on its own.
		SilenceUsage: true,
		// Answer lookups are not silent, so an unexpected error is printed once by
		// Execute rather than swallowed.
		SilenceErrors: false,
		Version:       Version(),
	}

	// Cobra's default template is "sael version 0.0.1-rc1". The version is the
	// whole output here, so it is printed alone: the installer that checks what it
	// just installed compares this string against a tag, and a word in the middle
	// of it is a difference nobody would think to strip.
	root.SetVersionTemplate("{{.Name}} {{.Version}}\n")

	root.AddCommand(newCheckCmd())
	root.AddCommand(newSetupCmd())

	return root
}
