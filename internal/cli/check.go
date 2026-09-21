package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/cipherTing/sael/internal/questions"
	"github.com/cipherTing/sael/systemone"
)

var (
	// errNoText is returned when the request text resolved to nothing, whichever
	// of the three sources it came from.
	errNoText = errors.New("no text to check")

	// errNoTextInteractive is the same situation with no input to read at all.
	// It is a separate error because the useful advice differs: with nothing
	// piped in, the fix is to supply a source.
	errNoTextInteractive = errors.New("no text to check: pass it as an argument, use --file, or pipe it in")
)

func newCheckCmd() *cobra.Command {
	var (
		file   string
		asJSON bool
	)

	cmd := &cobra.Command{
		Use:   "check [text]",
		Short: "Evaluate one request and print what the model reported",
		Long: `Send one request to the model and print every answer.

The text comes from the argument, from --file, or from standard input, so all of
these work:

  sael check "text to evaluate"
  sael check --file prompt.txt
  cat prompt.txt | sael check

At a terminal the answers are ranked and drawn, because the question being asked
is "did anything fire here". Piped into another program, or with --json, the same
answers come out as JSON that a script can parse.

No output carries a decision. Every answer is a measurement, and the threshold
that turns one into an action belongs to the caller.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			text, err := readText(cmd, args, file)
			if err != nil {
				return err
			}

			// An empty directory means the default location, so the caller needs no
			// path handling of its own.
			client, err := systemone.NewFromConfigDir("")
			if err != nil {
				return err
			}

			result, err := client.Evaluate(cmd.Context(), text, questions.Moderation())
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			// A pipe means something else is reading, so it gets JSON whether or not
			// anyone asked for it. Guessing wrong here is what makes a tool unusable
			// in a script.
			return writeResult(out, result, asJSON, isTerminal(out), colorEnabled())
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "read the request from this file instead of the argument or standard input")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON even at a terminal")

	return cmd
}

// readText resolves the request from the argument, a file, or standard input, in
// that order of precedence.
func readText(cmd *cobra.Command, args []string, file string) (string, error) {
	switch {
	case file != "":
		raw, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", file, err)
		}
		return requireText(strings.TrimSpace(string(raw)))

	case len(args) == 1:
		return requireText(strings.TrimSpace(args[0]))

	default:
		return readTextFrom(cmd.InOrStdin(), stdinIsTerminal())
	}
}

// readTextFrom reads the request from r.
//
// With no argument and nothing to read, reading a terminal would block until the
// caller closed it, so that case is refused with something actionable instead of
// hanging. Whether r is a terminal is passed in rather than detected here, which
// is what makes the refusing branch reachable from a test: a test process has no
// terminal on offer.
func readTextFrom(r io.Reader, terminal bool) (string, error) {
	if terminal {
		return "", errNoTextInteractive
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("reading standard input: %w", err)
	}
	return requireText(strings.TrimSpace(string(raw)))
}

func requireText(text string) (string, error) {
	if text == "" {
		return "", errNoText
	}
	return text, nil
}

// stdinIsTerminal reports whether standard input is a terminal.
func stdinIsTerminal() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// isTerminal reports whether w is a terminal. A writer that is not an *os.File
// cannot be one, which is the right answer for a buffer or a pipe.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// colorEnabled follows the NO_COLOR convention and otherwise colours only what a
// person is looking at. A dumb terminal gets no escapes either: it cannot render
// them, and the vendor that set TERM=dumb usually meant it.
func colorEnabled() bool {
	return colorEnabledFor(isTerminal(os.Stdout))
}

// colorEnabledFor applies the environment rules to an answer already known about
// whether the destination is a terminal. Taking that answer as an argument is
// what makes the precedence testable, and the precedence is the whole content of
// this function: the environment overrides the terminal, and NO_COLOR overrides
// TERM.
func colorEnabledFor(stdoutIsTerminal bool) bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return stdoutIsTerminal
}
