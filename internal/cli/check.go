package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/cipherTing/sael/internal/questions"
	"github.com/cipherTing/sael/systemone"
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
			if asJSON || !isTerminal(out) {
				return writeJSON(out, result)
			}

			return writeReport(out, result, colorEnabled())
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
		// With no argument and nothing on standard input, reading would block until
		// the caller closed the terminal. Say so instead of hanging.
		if term.IsTerminal(int(os.Stdin.Fd())) {
			return "", fmt.Errorf("no text to check: pass it as an argument, use --file, or pipe it in")
		}
		raw, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", fmt.Errorf("reading standard input: %w", err)
		}
		return requireText(strings.TrimSpace(string(raw)))
	}
}

func requireText(text string) (string, error) {
	if text == "" {
		return "", fmt.Errorf("no text to check")
	}
	return text, nil
}

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
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return isTerminal(os.Stdout)
}
