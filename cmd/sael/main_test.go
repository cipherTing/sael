package main

// main_test.go covers the process exit code, which is the entire contract between
// this command and a script.
//
// Every other behaviour is reachable from the cli package, but the exit status is
// not: os.Exit cannot be intercepted, so the only honest test is to run the real
// binary and look at what the shell would see.
//
// This gap was not theoretical. Replacing the body of main with
//
//	if err := cli.Execute(); err != nil {
//		fmt.Fprintln(os.Stderr, err)
//	}
//
// — an error printed and a zero exit — passes go vet, staticcheck and
// golangci-lint. A script branching on `$?` would read a failed check as a
// successful one, and nothing in the repository would object.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeResponse is a minimal success body, enough for the command to print
// something and exit zero.
const fakeResponse = `{
  "model": "typesafe/jev-1.13-20260917",
  "answers": {"fraud_deception": {"type": "noul", "noul": 0.97}},
  "usage": {"input_tokens": 620, "output_tokens": 4}
}`

func TestTheExitCodeIsWhatAScriptBranchesOn(t *testing.T) {
	binary := buildSael(t)

	t.Run("a command that fails exits non-zero", func(t *testing.T) {
		// Each case fails for a different reason and at a different stage: before
		// the command is resolved, during flag parsing, and while resolving the
		// text. A regression that made any one of them exit zero would silently
		// break `sael ... || echo failed`.
		cases := []struct {
			name string
			args []string
			says string
		}{
			{"a mistyped subcommand", []string{"chekc"}, "chekc"},
			{"an unknown flag", []string{"check", "--not-a-flag"}, "not-a-flag"},
			{"nothing to check", []string{"check"}, "no text to check"},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				stdout, stderr, exitCode := run(t, binary, nil, tc.args...)

				assert.NotEqual(t, 0, exitCode,
					"the process reported success for a command that failed")
				assert.Contains(t, stderr, tc.says,
					"the failure has to say what was wrong, or the non-zero exit is all "+
						"the operator gets")
				assert.Empty(t, stdout, "nothing belongs on stdout when the command failed")
			})
		}
	})

	t.Run("a check that succeeds exits zero", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, fakeResponse)
		}))
		t.Cleanup(srv.Close)

		// The config directory is empty and the credential comes from the
		// environment, so the subprocess neither reads nor writes ~/.Sael. Later
		// duplicates win, so these override anything inherited from the shell.
		env := []string{
			"SAEL_HOME=" + t.TempDir(),
			"TYPESAFE_BASE_URL=" + srv.URL,
			"TYPESAFE_API_KEY=test-key-not-a-real-credential",
			"TYPESAFE_DEFAULT_MODEL=jev-1.13.0",
		}

		stdout, stderr, exitCode := run(t, binary, env, "check", "hello")

		require.Equal(t, 0, exitCode, "a valid run exited non-zero; stderr was: %s", stderr)
		assert.Empty(t, stderr)

		// The subprocess's stdout is a pipe, so the command must have chosen JSON
		// on its own. This also proves the binary is the one just built.
		var decoded []struct {
			Question string  `json:"question"`
			Value    float64 `json:"value"`
		}
		require.NoError(t, json.Unmarshal([]byte(stdout), &decoded), "stdout was: %s", stdout)
		require.Len(t, decoded, 1)
		assert.Equal(t, "fraud_deception", decoded[0].Question)
		assert.InDelta(t, 0.97, decoded[0].Value, 1e-9)
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// run executes the binary and returns what the shell would see.
func run(t *testing.T, binary string, extraEnv []string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()

	cmd := exec.Command(binary, args...)
	// Standard input left unset means the child reads /dev/null: an empty pipe
	// rather than a terminal, which is the state a script runs in.
	cmd.Env = append(os.Environ(), extraEnv...)

	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf

	err := cmd.Run()
	if err == nil {
		return out.String(), errBuf.String(), 0
	}

	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr, "the binary could not be started at all: %v", err)
	return out.String(), errBuf.String(), exitErr.ExitCode()
}

// buildSael builds the command once per test run and returns the path to it.
func buildSael(t *testing.T) string {
	t.Helper()

	goTool := goBinary(t)
	dir := t.TempDir()

	// Passing a directory to -o lets the toolchain choose the file name, which is
	// what keeps this test free of a Windows .exe special case.
	cmd := exec.Command(goTool, "build", "-o", dir, "./cmd/sael")
	cmd.Dir = filepath.Join("..", "..")

	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "go build failed:\n%s", output)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "expected exactly one binary in %s, found %d", dir, len(entries))

	return filepath.Join(dir, entries[0].Name())
}

// goBinary finds the toolchain that is running this test.
func goBinary(t *testing.T) string {
	t.Helper()

	if path, err := exec.LookPath("go"); err == nil {
		return path
	}

	// The toolchain that compiled this test is a reliable fallback for a PATH that
	// has been stripped, which is common in CI and in editor integrations.
	fallback := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		fallback += ".exe"
	}
	if _, err := os.Stat(fallback); err == nil {
		return fallback
	}

	t.Skip("no Go toolchain found to build the binary with")
	return ""
}
