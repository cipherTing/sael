package cli

// root_test.go covers the command tree itself: what it advertises, how it fails,
// and the one thing main depends on — that a failure comes back as an error
// rather than being swallowed.
//
// The exit code is the contract here. A script that runs sael and branches on
// `$?` only learns something went wrong if Execute returns the error and main
// turns it into a non-zero exit.

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRootCommandDescribesItself(t *testing.T) {
	root := newRootCmd()

	assert.Equal(t, "sael", root.Use)
	assert.NotEmpty(t, root.Short, "the one-line summary is what `sael help` lists")
	assert.NotEmpty(t, root.Long,
		"the long description is where the tool states that it reports measurements "+
			"rather than decisions, which is the thing a caller most needs to know")

	// The long text is the first documentation a user reads, so the promise it
	// makes has to stay in it.
	assert.Contains(t, root.Long, "measurements",
		"the tool stopped describing itself as a measurement")
	assert.Contains(t, root.Long, "caller",
		"the description no longer says who owns the threshold")
}

func TestRootCommandRegistersCheck(t *testing.T) {
	root := newRootCmd()

	found, _, err := root.Find([]string{"check"})
	require.NoError(t, err)
	assert.Equal(t, "check", found.Name(), "Find resolved to something else")
}

func TestCheckFlagsAreTheOnesUsersTypeIntoTheirShell(t *testing.T) {
	check := newCheckCmd()

	file := check.Flags().Lookup("file")
	require.NotNil(t, file, "--file disappeared")
	assert.Equal(t, "f", file.Shorthand, "-f is the shorthand users already type")
	assert.Empty(t, file.DefValue, "no file means the argument or stdin is used instead")

	asJSON := check.Flags().Lookup("json")
	require.NotNil(t, asJSON, "--json disappeared")
	assert.Equal(t, "false", asJSON.DefValue,
		"JSON is opt-in at a terminal, where the ranked view is the useful default")
	assert.Empty(t, asJSON.Shorthand,
		"--json has no shorthand, so no single letter silently changes the output shape")

	assert.Equal(t, "check [text]", check.Use)
}

// TestAFlagErrorNamesTheFlagWithoutDumpingUsage covers SilenceUsage, which is one
// line in a struct literal and whose absence is invisible until someone mistypes
// a flag.
func TestAFlagErrorNamesTheFlagWithoutDumpingUsage(t *testing.T) {
	stdout, stderr, err := runRoot(t, "check", "--not-a-flag")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not-a-flag",
		"the error must name what was wrong, because main may print only the error")

	assert.Contains(t, stderr, "not-a-flag")
	assert.NotContains(t, stderr, "Usage:",
		"SilenceUsage keeps the usage block out of a flag error: the flag is already "+
			"named, and a screen of usage buries it")
	assert.Empty(t, stdout, "nothing belongs on stdout when the command never ran")
}

func TestAnUnknownSubcommandFails(t *testing.T) {
	_, _, err := runRoot(t, "chekc")

	require.Error(t, err, "a mistyped subcommand must not exit zero, or a typo in a "+
		"script would look like a successful run")
	assert.Contains(t, err.Error(), "chekc", "the error names what was typed")
}

// TestExecuteReturnsTheErrorMainNeedsToSee is the guard on the exit code.
//
// Execute is `newRootCmd().Execute()`, and main turns its error into os.Exit(1).
// If Execute ever discarded the error, every sael run would exit zero, and a
// script branching on the exit code would read a failed check as a successful one
// with nothing to report.
func TestExecuteReturnsTheErrorMainNeedsToSee(t *testing.T) {
	withArgs(t, "sael", "check", "--not-a-flag")

	var err error
	_, stderr := captureStreams(t, func() { err = Execute() })

	require.Error(t, err, "Execute swallowed the failure, so main would exit 0 on a "+
		"broken run")
	assert.Contains(t, stderr, "not-a-flag")
}

// TestExecuteSucceedsWithAValidCommandLine checks the other direction, so that an
// Execute returning a non-nil error unconditionally could not pass the test above.
func TestExecuteSucceedsWithAValidCommandLine(t *testing.T) {
	withArgs(t, "sael", "check", "hello")

	// A service that answers, so the run reaches the success path. The reply is
	// minimal because this test is about the exit status, not about the answer.
	fakeService(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, fakeResponse)
	})

	var err error
	stdout, stderr := captureStreams(t, func() { err = Execute() })

	require.NoError(t, err, "a valid run reported a failure; stderr was: %s", stderr)
	assert.Contains(t, stdout, "fraud_deception",
		"the answers should have been written to stdout")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// runRoot runs the command tree with arbitrary arguments and captures both
// streams. runCheck in check_test.go is the same thing with "check" prefixed.
func runRoot(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	root := newRootCmd()

	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetIn(bytes.NewReader(nil))
	root.SetArgs(args)

	err = root.Execute()
	return out.String(), errBuf.String(), err
}

// withArgs replaces the process arguments for the duration of the test, which is
// the only way to drive Execute: it builds its own command and reads os.Args
// itself.
func withArgs(t *testing.T, args ...string) {
	t.Helper()

	saved := os.Args
	os.Args = args
	t.Cleanup(func() { os.Args = saved })
}

// captureStreams points the process's standard output and error at temporary
// files for the duration of fn and returns what was written to each.
//
// Execute picks its own writers, so the other tests' buffer trick does not reach
// it. Redirecting also keeps the error cobra prints out of the test log.
func captureStreams(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()

	dir := t.TempDir()
	outFile, err := os.CreateTemp(dir, "stdout")
	require.NoError(t, err)
	errFile, err := os.CreateTemp(dir, "stderr")
	require.NoError(t, err)

	savedOut, savedErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outFile, errFile

	fn()

	os.Stdout, os.Stderr = savedOut, savedErr
	require.NoError(t, outFile.Close())
	require.NoError(t, errFile.Close())

	return readFile(t, outFile.Name()), readFile(t, errFile.Name())
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(content)
}
