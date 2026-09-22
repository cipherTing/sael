package cli

// check_test.go covers the check command: where the text comes from, what goes
// out to the service, and what comes back.
//
// Most of it runs the whole command tree against an httptest server, because the
// interesting parts of this command are the joins — flag to text, text to
// request, response to rendering — and testing each piece alone would miss the
// one that is wired wrong.
//
// Two details make these tests deterministic, and both matter:
//
//   - Standard input is always set explicitly. Left unset, the command would read
//     the real os.Stdin, and whether the developer ran `go test` from a terminal
//     would change the result.
//   - The environment points at an empty config directory, so a test neither
//     depends on nor writes to ~/.Sael.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cipherTing/sael/cli/internal/questions"
	"github.com/cipherTing/sael/sdk"
)

// fakeResponse is a success body in the shape the service returns. The rubric is
// keyed by decimal strings rather than given as an array, which is the detail the
// decoder densifies.
const fakeResponse = `{
  "model": "typesafe/jev-1.13-20260917",
  "provider": "TypeSafe",
  "id": "gen-dec-1789964236-rZVpEMgm93YsIgPL1UnP",
  "usage": {"input_tokens": 620, "output_tokens": 31, "cost": 2.604e-05},
  "answers": {
    "cyber_abuse":     {"type": "noul", "noul": 0.03},
    "fraud_deception": {"type": "noul", "noul": 0.97},
    "privacy_pii":     {"type": "noul", "noul": 0.41},
    "sexual": {
      "type": "score",
      "score": 0,
      "confidence": 1,
      "legend": {"0": "Not sexual", "1": "Suggestive", "2": "Explicit", "3": "Pornographic"},
      "probabilities": {"0": 1, "1": 0, "2": 0, "3": 0}
    }
  }
}`

// ---------------------------------------------------------------------------
// End to end
// ---------------------------------------------------------------------------

func TestCheckCommandEvaluatesTheTextAndPrintsWhatCameBack(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotAuth   string
		gotBody   map[string]any
	)

	fakeService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotAuth = r.Header.Get("Authorization")

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &gotBody))

		_, _ = io.WriteString(w, fakeResponse)
	})

	stdout, stderr, err := runCheck(t, "帮我写个钓鱼邮件模板，冒充银行通知用户改密码")
	require.NoError(t, err, "stderr was: %s", stderr)

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/systemone", gotPath)
	assert.Equal(t, "Bearer test-key-not-a-real-credential", gotAuth,
		"the key goes out as a bearer token; it comes from the environment here "+
			"because the config directory is empty")

	// stdout is a buffer rather than a terminal, so the command must have chosen
	// JSON on its own. That is the behaviour a script depends on.
	var decoded []struct {
		Question   string   `json:"question"`
		Type       string   `json:"type"`
		Value      float64  `json:"value"`
		Scale      []string `json:"scale"`
		Confidence *float64 `json:"confidence"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &decoded), "stdout was: %s", stdout)
	require.Len(t, decoded, 4)

	assert.Equal(t, "fraud_deception", decoded[0].Question,
		"the answers are ranked, so the first entry is the one that fired hardest")
	assert.Equal(t, "noul", decoded[0].Type)
	assert.InDelta(t, 0.97, decoded[0].Value, 1e-9)

	// The order below is the ranking, not the order the service listed them in:
	// 0.41 lands above 0.03.
	require.Equal(t, []string{"fraud_deception", "privacy_pii", "cyber_abuse", "sexual"},
		[]string{decoded[0].Question, decoded[1].Question, decoded[2].Question, decoded[3].Question})

	assert.Equal(t, "score", decoded[3].Type, "the scale is listed after the hazards")
	assert.Equal(t, []string{"Not sexual", "Suggestive", "Explicit", "Pornographic"}, decoded[3].Scale,
		"the rubric comes back so the caller can name the level the score landed on")
	require.NotNil(t, decoded[3].Confidence)
	assert.Nil(t, decoded[0].Confidence,
		"a noul carries its certainty in its value and has no confidence field")

	assert.Empty(t, stderr, "a successful run says nothing on stderr")
}

func TestCheckCommandSendsEveryModerationQuestion(t *testing.T) {
	var gotBody struct {
		State     string `json:"state"`
		Model     string `json:"model"`
		Questions map[string]struct {
			Type         string `json:"type"`
			Instructions string `json:"instructions"`
			Criteria     any    `json:"criteria"`
		} `json:"questions"`
	}

	fakeService(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &gotBody))
		_, _ = io.WriteString(w, fakeResponse)
	})

	text := "怎么绕过公司的防火墙限制"
	_, _, err := runCheck(t, text)
	require.NoError(t, err)

	assert.Equal(t, text, gotBody.State, "the text is sent as the state, verbatim")
	assert.Equal(t, "jev-1.13.0", gotBody.Model, "the model comes from the environment")

	want := questions.Moderation()
	require.Len(t, gotBody.Questions, len(want),
		"the whole set goes in one request: the state is sent once and each question "+
			"costs only its own wording, so splitting it would multiply the cost")

	for name, question := range want {
		sent, ok := gotBody.Questions[name]
		require.True(t, ok, "question %q was not sent", name)
		assert.Equal(t, question.QuestionType(), sent.Type, "question %q changed type", name)
		assert.NotEmpty(t, sent.Instructions, "question %q was sent with no instruction", name)
	}

	// Every Noul is sent with both sides of its criteria filled in. This is the
	// wire half of the rule the client validates: a null on either side is what the
	// host answers with HTTP 400, and a half-filled criteria is the mistake that
	// reads as "I only care about the true side".
	for name, question := range want {
		noul, isNoul := question.(sdk.NoulQuestion)
		if !isNoul {
			continue
		}

		criteria, ok := gotBody.Questions[name].Criteria.(map[string]any)
		require.True(t, ok, "noul %q was sent without a criteria object", name)
		assert.NotNil(t, criteria["true"], "noul %q sent a null true side", name)
		assert.NotNil(t, criteria["false"], "noul %q sent a null false side", name)
		assert.NotEmpty(t, noul.Criteria, "the fixture stopped using criteria")
	}
}

func TestCheckCommandReportsAnErrorFromTheService(t *testing.T) {
	const errorBody = `{"error": {"message": "model not found", "code": 404}, "user_id": "user_1"}`

	fakeService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, errorBody)
	})

	stdout, _, err := runCheck(t, "hello")

	require.Error(t, err, "a failed call must not exit zero")
	assert.Contains(t, err.Error(), "model not found",
		"the service's message is what tells the operator what to fix, so it has to "+
			"reach the error rather than be replaced by a status code")
	assert.Empty(t, stdout, "the success rendering must not appear when the call failed")
}

// TestCheckCommandPutsNoDecisionInTheOutput pins the product rule where a
// regression would be visible from outside.
//
// sael reports measurements and leaves the threshold to the relay. A "verdict" or
// "action" field would be the beginning of deciding, and it would also make the
// separate evaluation command measure our thresholds instead of the model.
func TestCheckCommandPutsNoDecisionInTheOutput(t *testing.T) {
	fakeService(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, fakeResponse)
	})

	stdout, _, err := runCheck(t, "hello")
	require.NoError(t, err)

	// Every key anywhere in the document has to be one the schema documents. That
	// covers "no extra fields" and, with it, no verdict.
	var entries []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &entries))
	require.NotEmpty(t, entries)

	allowed := map[string]bool{
		"question": true, "type": true, "value": true, "scale": true, "confidence": true,
	}
	for i, entry := range entries {
		for key := range entry {
			assert.True(t, allowed[key], "entry %d carries the undocumented field %q", i, key)
		}
	}
}

// TestHumanViewHasNoDecisionBanner checks the same rule for the other rendering.
//
// The shape is the assertion: every non-empty line belongs to the indented answer
// block, so a line announcing an outcome would be the only kind of thing that
// could break it.
func TestHumanViewHasNoDecisionBanner(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, writeResult(&buf, sampleResult(), false, true, false))

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	require.Len(t, lines, 4, "two hazards, a separator and one scale")

	for _, line := range lines {
		if line == "" {
			continue
		}
		assert.True(t, strings.HasPrefix(line, "  "),
			"every line is an indented answer, so a verdict banner would stand out "+
				"here: %q", line)
	}
}

// ---------------------------------------------------------------------------
// Where the text comes from
// ---------------------------------------------------------------------------

func TestCheckCommandReadsStandardInputWhenPiped(t *testing.T) {
	var gotState string

	fakeService(t, func(w http.ResponseWriter, r *http.Request) {
		gotState = stateOf(t, r)
		_, _ = io.WriteString(w, fakeResponse)
	})

	stdout, _, err := runCheckStdin(t, "  a piped request\n\n")
	require.NoError(t, err)
	assert.Equal(t, "a piped request", gotState,
		"surrounding whitespace is trimmed, because a piped file almost always ends "+
			"in a newline and sending it changes the state the model reads")
	assert.NotEmpty(t, stdout)
}

func TestCheckCommandReadsAFile(t *testing.T) {
	path := writeTempFile(t, "  from a file\n")

	var gotState string
	fakeService(t, func(w http.ResponseWriter, r *http.Request) {
		gotState = stateOf(t, r)
		_, _ = io.WriteString(w, fakeResponse)
	})

	_, _, err := runCheckStdin(t, "", "--file", path)
	require.NoError(t, err)
	assert.Equal(t, "from a file", gotState)
}

func TestCheckCommandPrefersTheFileOverTheArgument(t *testing.T) {
	path := writeTempFile(t, "the file wins")

	var gotState string
	fakeService(t, func(w http.ResponseWriter, r *http.Request) {
		gotState = stateOf(t, r)
		_, _ = io.WriteString(w, fakeResponse)
	})

	// With both sources given, the flag wins. The alternative would make
	// `sael check --file x.txt leftover` ignore the flag, and the argument is the
	// shorthand while --file is the explicit instruction.
	_, _, err := runCheckStdin(t, "", "--file", path, "the argument")
	require.NoError(t, err)
	assert.Equal(t, "the file wins", gotState)
}

func TestCheckCommandReportsAFileItCannotRead(t *testing.T) {
	fakeService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the service was called for a request that could not be read")
		_, _ = io.WriteString(w, fakeResponse)
	})

	missing := filepath.Join(t.TempDir(), "nope.txt")
	_, _, err := runCheck(t, "--file", missing)

	require.Error(t, err)
	assert.Contains(t, err.Error(), missing,
		"the error has to name the path, or the operator cannot tell a typo from a "+
			"permissions problem")
}

func TestCheckCommandReportsAFileThatIsADirectory(t *testing.T) {
	fakeService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the service was called for a request that could not be read")
		_, _ = io.WriteString(w, fakeResponse)
	})

	_, _, err := runCheck(t, "--file", t.TempDir())
	require.Error(t, err, "reading a directory must fail, not send an empty request")
}

func TestCheckCommandWithNoTextAtAll(t *testing.T) {
	fakeService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the service was called with nothing to check")
		_, _ = io.WriteString(w, fakeResponse)
	})

	cases := []struct {
		name  string
		stdin string
		args  []string
	}{
		{"nothing piped in", "", nil},
		{"whitespace only on stdin", "   \n\t\n", nil},
		{"an argument that is only whitespace", "ignored", []string{"   "}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := runCheckStdin(t, tc.stdin, tc.args...)
			require.ErrorIs(t, err, errNoText,
				"an empty request would still be billed, and every probability would "+
					"come back low, which reads like a clean result")
		})
	}
}

func TestCheckCommandRejectsMoreThanOneArgument(t *testing.T) {
	fakeService(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, fakeResponse)
	})

	_, _, err := runCheck(t, "one", "two")
	require.Error(t, err,
		"two arguments is a quoting mistake, and joining them would send a request "+
			"nobody wrote")
}

// ---------------------------------------------------------------------------
// Configuration that cannot be used
// ---------------------------------------------------------------------------

// TestCheckCommandReportsAConfigurationItCannotUse covers the failure that
// happens before any request is built.
//
// Both cases have to fail during construction, with a message about the
// configuration. The alternative is what makes a misconfigured CLI miserable to
// diagnose: the run proceeds, the service answers 401 or the connection is
// refused, and the operator is left reading a transport error for a problem in a
// file they never looked at.
func TestCheckCommandReportsAConfigurationItCannotUse(t *testing.T) {
	t.Run("no API key anywhere", func(t *testing.T) {
		t.Setenv("SAEL_HOME", t.TempDir())
		unsetEnv(t, "TYPESAFE_API_KEY")
		// A port nothing listens on. If the client reached the network despite the
		// missing key, the error would be a connection failure instead.
		t.Setenv("TYPESAFE_BASE_URL", "http://127.0.0.1:1/systemone")

		_, _, err := runCheck(t, "hello")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no API key")
		assert.Contains(t, err.Error(), "TYPESAFE_API_KEY",
			"the error has to name the variable to set, or the operator has to go "+
				"reading the docs to find out what is missing")
		assert.NotContains(t, err.Error(), "connection refused",
			"the request was sent despite there being no credential")
	})

	t.Run("a config file that does not parse", func(t *testing.T) {
		home := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(home, "config.json"), []byte(`{"base_url": `), 0o600))
		t.Setenv("SAEL_HOME", home)
		t.Setenv("TYPESAFE_API_KEY", "test-key-not-a-real-credential")

		_, _, err := runCheck(t, "hello")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "config.json",
			"a file that exists and does not parse is an error, and the error has to "+
				"say which file")
	})

	t.Run("a base URL that is not absolute", func(t *testing.T) {
		t.Setenv("SAEL_HOME", t.TempDir())
		t.Setenv("TYPESAFE_API_KEY", "test-key-not-a-real-credential")
		t.Setenv("TYPESAFE_BASE_URL", "openrouter.ai/api/v1")

		_, _, err := runCheck(t, "hello")

		require.Error(t, err, "a base URL without a scheme must fail here rather than "+
			"produce a request that goes nowhere")
		assert.Contains(t, err.Error(), "absolute")
	})
}

// ---------------------------------------------------------------------------
// readText, directly
// ---------------------------------------------------------------------------

func TestReadTextSourcePrecedence(t *testing.T) {
	path := writeTempFile(t, "from the file")

	cases := []struct {
		name  string
		file  string
		args  []string
		stdin string
		want  string
	}{
		{"file beats the argument", path, []string{"from the argument"}, "from stdin", "from the file"},
		{"the argument beats stdin", "", []string{"from the argument"}, "from stdin", "from the argument"},
		{"stdin when nothing else is given", "", nil, "from stdin", "from stdin"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newCheckCmd()
			cmd.SetIn(strings.NewReader(tc.stdin))

			got, err := readText(cmd, tc.args, tc.file)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestReadTextTrimsSurroundingWhitespace(t *testing.T) {
	cmd := newCheckCmd()
	cmd.SetIn(strings.NewReader("ignored"))

	got, err := readText(cmd, []string{"  spaced out  \n"}, "")
	require.NoError(t, err)
	assert.Equal(t, "spaced out", got)
}

func TestReadTextLeavesInnerWhitespaceAlone(t *testing.T) {
	cmd := newCheckCmd()
	cmd.SetIn(strings.NewReader("ignored"))

	// Only the edges are trimmed. Reflowing the middle would change the state the
	// model reads, and inside a code block or a poem that matters.
	text := "alpha beta\n\n    four spaces of indent\ngamma delta"
	got, err := readText(cmd, []string{text}, "")
	require.NoError(t, err)
	assert.Equal(t, text, got)
}

func TestReadTextReportsAReadFailure(t *testing.T) {
	cmd := newCheckCmd()
	cmd.SetIn(failingReader{})

	_, err := readText(cmd, nil, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading standard input")
	assert.Contains(t, err.Error(), "disk on fire", "the underlying cause is kept")
}

func TestReadTextFromRefusesToBlockOnATerminal(t *testing.T) {
	// With nothing piped in and no argument, reading a terminal would wait for the
	// caller to press Ctrl-D. Saying so is the difference between a tool that looks
	// hung and one that explains itself.
	_, err := readTextFrom(strings.NewReader("unread"), true)

	require.ErrorIs(t, err, errNoTextInteractive)
	assert.Contains(t, err.Error(), "--file",
		"the message names the alternatives, not just the problem")
}

func TestReadTextFromReadsANonTerminal(t *testing.T) {
	got, err := readTextFrom(strings.NewReader("  piped  \n"), false)
	require.NoError(t, err)
	assert.Equal(t, "piped", got)
}

func TestReadTextFromTreatsAnEmptyNonTerminalAsEmpty(t *testing.T) {
	// /dev/null and a pipe from a program that wrote nothing are the same thing
	// here, and both mean there is nothing to check.
	_, err := readTextFrom(strings.NewReader(""), false)
	require.ErrorIs(t, err, errNoText)
}

// ---------------------------------------------------------------------------
// Terminal detection and colour
// ---------------------------------------------------------------------------

func TestIsTerminalIsFalseForAnythingButATerminalFile(t *testing.T) {
	// Answering true for a buffer would draw bars into a pipe, which is the one
	// guess that makes a tool unusable in a script.
	assert.False(t, isTerminal(&bytes.Buffer{}), "a buffer is not a terminal")

	file, err := os.CreateTemp(t.TempDir(), "out")
	require.NoError(t, err)
	defer func() { require.NoError(t, file.Close()) }()

	assert.False(t, isTerminal(file),
		"a regular file is not a terminal even though it is an *os.File")

	// A writer that is not an *os.File cannot be a terminal, and the type
	// assertion has to answer rather than panic.
	assert.False(t, isTerminal(failingWriter{}))
	assert.False(t, isTerminal(io.Discard), "io.Discard is not an *os.File")
}

func TestColorEnabledForFollowsTheEnvironment(t *testing.T) {
	cases := []struct {
		name       string
		noColor    string
		noColorSet bool
		term       string
		terminal   bool
		want       bool
	}{
		{
			name: "a terminal, nothing set", term: "xterm-256color", terminal: true, want: true,
		},
		{
			name: "not a terminal", term: "xterm-256color", terminal: false, want: false,
			// The destination cannot render escapes, so emitting them would put
			// garbage in a file or a pipe.
		},
		{
			name: "a dumb terminal", term: "dumb", terminal: true, want: false,
			// TERM=dumb means the destination cannot render them, and whoever set
			// it meant it.
		},
		{
			name: "NO_COLOR set to a value", noColor: "1", noColorSet: true,
			term: "xterm-256color", terminal: true, want: false,
		},
		{
			name: "NO_COLOR set to empty", noColor: "", noColorSet: true,
			term: "xterm-256color", terminal: true, want: false,
			// The convention is that the variable's presence is the signal and its
			// value is not read, so NO_COLOR= is how a script disables colour.
		},
		{
			name: "NO_COLOR beats a colour-capable terminal", noColor: "1", noColorSet: true,
			term: "xterm-256color", terminal: true, want: false,
		},
		{
			name: "NO_COLOR and TERM=dumb together", noColor: "1", noColorSet: true,
			term: "dumb", terminal: false, want: false,
		},
		{
			name: "TERM=dumb without NO_COLOR", term: "dumb", terminal: false, want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// An unset variable is restored by unsetting it, so the three states
			// (absent, empty, set) stay distinct.
			if tc.noColorSet {
				t.Setenv("NO_COLOR", tc.noColor)
			} else {
				unsetEnv(t, "NO_COLOR")
			}
			if tc.term == "" {
				unsetEnv(t, "TERM")
			} else {
				t.Setenv("TERM", tc.term)
			}

			assert.Equal(t, tc.want, colorEnabledFor(tc.terminal))
		})
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// fakeService starts a server and points the command at it.
//
// The config directory is an empty temporary one, so a developer's real ~/.Sael is
// never read, and the credentials come from the environment, which is the layer
// that overrides the file.
func fakeService(t *testing.T, handler http.HandlerFunc) {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	t.Setenv("TYPESAFE_BASE_URL", srv.URL)
	t.Setenv("TYPESAFE_API_KEY", "test-key-not-a-real-credential")
	t.Setenv("TYPESAFE_DEFAULT_MODEL", "jev-1.13.0")
	t.Setenv("SAEL_HOME", t.TempDir())
}

// runCheck runs the whole command tree with standard input connected to an empty
// reader, which is what "nothing was piped in" looks like when standard input is
// not a terminal.
func runCheck(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return runCheckStdin(t, "", args...)
}

// runCheckStdin runs the whole command tree with stdin as its input.
//
// Setting stdin is not optional bookkeeping: leaving it unset makes the command
// read the real os.Stdin, so the outcome would depend on whether the developer ran
// `go test` from a terminal.
func runCheckStdin(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	root := newRootCmd()

	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(append([]string{"check"}, args...))

	err = root.Execute()
	return out.String(), errBuf.String(), err
}

// stateOf reads the state a request carried, which is the field the three input
// paths are distinguished by.
func stateOf(t *testing.T, r *http.Request) string {
	t.Helper()

	raw, err := io.ReadAll(r.Body)
	require.NoError(t, err)

	var body struct {
		State string `json:"state"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	return body.State
}

// writeTempFile writes content to a file the test owns and returns its path.
func writeTempFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "prompt.txt")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// unsetEnv removes a variable for the duration of the test. t.Setenv cannot
// express "absent", because setting a variable to "" is a third state that
// NO_COLOR treats differently.
func unsetEnv(t *testing.T, key string) {
	t.Helper()

	previous, existed := os.LookupEnv(key)
	require.NoError(t, os.Unsetenv(key))

	t.Cleanup(func() {
		if existed {
			require.NoError(t, os.Setenv(key, previous))
			return
		}
		require.NoError(t, os.Unsetenv(key))
	})
}

// failingReader stands in for a read error, which standard input can produce if
// the pipe is closed mid-read.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("reading: disk on fire")
}
