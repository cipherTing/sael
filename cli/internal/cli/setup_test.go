package cli

// setup_test.go covers the onboarding command: what it asks, what it writes, and
// what it refuses to write.
//
// Two rules shape every test here:
//
//   - Nothing touches the real network. The verification call is the part of
//     setup that reaches out, so the service is an httptest server and the base
//     URL always points at it — except where a test is proving that no call is
//     made at all, which it proves by failing if one is.
//   - Nothing touches the real ~/.Sael. SAEL_HOME is a fresh directory per test,
//     under the default-name subdirectory so that the 0700 claim is about a
//     directory setup created rather than one t.TempDir made.
//
// The prompts are driven through the reader rather than a terminal, because the
// branch that matters most — refusing to ask when standard input is not a
// terminal — is the one a test process is already in.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cipherTing/sael/sdk"
)

// ---------------------------------------------------------------------------
// The happy path
// ---------------------------------------------------------------------------

// TestSetupWritesAConfigurationEveryOtherCommandCanRead is the end-to-end check:
// the probe goes out, the files come back through the SDK's own loaders, and the
// modes are the private ones a credential file needs.
func TestSetupWritesAConfigurationEveryOtherCommandCanRead(t *testing.T) {
	dir := setupTestEnv(t)

	var (
		gotMethod string
		gotPath   string
		gotAuth   string
		gotState  string
		gotModel  string
		gotBody   map[string]any
	)
	srvURL := fakeSetupService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotAuth = r.Header.Get("Authorization")

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &gotBody))
		gotState, _ = gotBody["state"].(string)
		gotModel, _ = gotBody["model"].(string)

		_, _ = io.WriteString(w, fakeResponse)
	})

	stdout, stderr, err := runSetup(t, "",
		"--yes", "--skip-verify=false",
		"--api-key", "flag-key-not-a-real-credential",
		"--base-url", srvURL,
		"--model", "jev-1.13.0")
	require.NoError(t, err, "stderr was: %s", stderr)
	assert.Empty(t, stderr, "a successful setup says nothing on stderr")

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/systemone", gotPath,
		"the probe goes to the same endpoint a check does, so it proves the same path")
	assert.Equal(t, "Bearer flag-key-not-a-real-credential", gotAuth,
		"the probe has to use the key that is about to be written, not one from the file")
	assert.NotEmpty(t, gotState)
	assert.Equal(t, "jev-1.13.0", gotModel)

	// A probe asks one question. It is a real, billed call, so letting it grow
	// into the moderation set would turn every onboarding into a full request.
	questions, ok := gotBody["questions"].(map[string]any)
	require.True(t, ok, "the probe sent no questions at all")
	require.Len(t, questions, 1, "the probe is meant to be the smallest request that proves the key")
	for _, q := range questions {
		asMap, ok := q.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "noul", asMap["type"])
	}

	// The files come back through sdk.LoadConfig and sdk.LoadAuth, which is the
	// point of writing them with the SDK: one implementation of the format, and
	// one place where the modes are decided.
	cfg, found, err := sdk.LoadConfig(dir)
	require.NoError(t, err)
	require.True(t, found, "setup did not write config.json")
	assert.Equal(t, srvURL, cfg.BaseURL)
	assert.Equal(t, "jev-1.13.0", cfg.Model)

	auth, found, err := sdk.LoadAuth(dir)
	require.NoError(t, err)
	require.True(t, found, "setup did not write auth.json")
	assert.Equal(t, "flag-key-not-a-real-credential", auth.APIKey)

	assertMode(t, dir, 0o700)
	assertMode(t, filepath.Join(dir, sdk.ConfigFileName), 0o600)
	assertMode(t, filepath.Join(dir, sdk.AuthFileName), 0o600)

	// The install script shows these paths, so the command has to name them rather
	// than say that something was written somewhere.
	assert.Contains(t, stdout, filepath.Join(dir, sdk.ConfigFileName))
	assert.Contains(t, stdout, filepath.Join(dir, sdk.AuthFileName))
}

// ---------------------------------------------------------------------------
// The probe
// ---------------------------------------------------------------------------

func TestSetupRefusesToWriteAKeyTheServiceRejects(t *testing.T) {
	dir := setupTestEnv(t)

	const errorBody = `{"error": {"message": "invalid api key", "code": 401}}`
	srvURL := fakeSetupService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, errorBody)
	})

	_, _, err := runSetup(t, "", "--yes",
		"--api-key", "wrong-key", "--base-url", srvURL)

	require.Error(t, err, "a key the service refuses must not be written as if it worked")
	assert.Contains(t, err.Error(), "refused the API key")
	assert.Contains(t, err.Error(), "invalid api key",
		"the service's own message is what tells the operator whether the key is "+
			"wrong or the account is out of credit")
	assert.Contains(t, err.Error(), "nothing was written")

	assertNoConfiguration(t, dir)
}

func TestSetupReportsAHostItCannotReach(t *testing.T) {
	dir := setupTestEnv(t)

	// A port nothing listens on. The retry policy is off for the probe, so the
	// refusal comes back at once instead of after the whole backoff ladder.
	_, _, err := runSetup(t, "", "--yes",
		"--api-key", "some-key", "--base-url", "http://127.0.0.1:1/v1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not reach")
	assert.Contains(t, err.Error(), "--skip-verify",
		"an unreachable host is the failure an operator is most likely to hit on a "+
			"locked-down network, and the way past it belongs in the message")
	assert.Contains(t, err.Error(), "nothing was written")

	assertNoConfiguration(t, dir)
}

func TestSetupSkipVerifyWritesWithoutCallingTheService(t *testing.T) {
	dir := setupTestEnv(t)

	srvURL := fakeSetupService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("--skip-verify made a request")
		_, _ = io.WriteString(w, fakeResponse)
	})

	_, _, err := runSetup(t, "", "--yes", "--skip-verify",
		"--api-key", "unverified-key", "--base-url", srvURL)

	require.NoError(t, err)

	cfg, _, err := sdk.LoadConfig(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, srvURL, cfg.BaseURL)
}

// ---------------------------------------------------------------------------
// --yes: the non-interactive form
// ---------------------------------------------------------------------------

func TestSetupYesWithoutFlagsTakesEveryDefault(t *testing.T) {
	dir := setupTestEnv(t)

	stdout, _, err := runSetup(t, "", "--yes", "--skip-verify", "--api-key", "a-key")
	require.NoError(t, err)

	cfg, _, err := sdk.LoadConfig(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, sdk.DefaultBaseURL, cfg.BaseURL)
	assert.Equal(t, defaultSetupModel, cfg.Model)

	assert.NotContains(t, stdout, "Base URL [",
		"--yes means no questions are asked, and a prompt that appeared anyway would "+
			"be answered by whatever happened to be on standard input")
	assert.NotContains(t, stdout, "API key:")
}

func TestSetupYesWithoutAKeyFails(t *testing.T) {
	dir := setupTestEnv(t)

	_, _, err := runSetup(t, "", "--yes", "--skip-verify")

	require.ErrorIs(t, err, errSetupNoAPIKey)
	assert.Contains(t, err.Error(), "--api-key")
	assert.Contains(t, err.Error(), envSetupAPIKey,
		"the error has to name both ways to supply the key, or the operator has to go "+
			"looking for them")
	assertNoConfiguration(t, dir)
}

func TestSetupWithoutATerminalRefusesInsteadOfWritingDefaults(t *testing.T) {
	dir := setupTestEnv(t)

	// Answers are on standard input, and this is still not a terminal: reading
	// them would write whatever a pipe happened to contain. The same mistake in an
	// install script is invisible until every request comes back 401.
	stdout, _, err := runSetup(t, "\n\nsome-key\n", "--skip-verify")

	require.ErrorIs(t, err, errSetupNotInteractive)
	assert.Contains(t, err.Error(), "--yes",
		"the message names the way to run it unattended, not just the problem")
	assert.Empty(t, stdout)
	assertNoConfiguration(t, dir)
}

// ---------------------------------------------------------------------------
// Where the key comes from
// ---------------------------------------------------------------------------

func TestSetupReadsTheKeyFromTheEnvironment(t *testing.T) {
	dir := setupTestEnv(t)
	t.Setenv(envSetupAPIKey, "env-key-not-a-real-credential")

	_, _, err := runSetup(t, "", "--yes", "--skip-verify")
	require.NoError(t, err)

	auth, _, err := sdk.LoadAuth(dir)
	require.NoError(t, err)
	require.NotNil(t, auth)
	assert.Equal(t, "env-key-not-a-real-credential", auth.APIKey)
}

func TestSetupPrefersTheFlagOverTheEnvironment(t *testing.T) {
	dir := setupTestEnv(t)
	t.Setenv(envSetupAPIKey, "env-key")

	_, _, err := runSetup(t, "", "--yes", "--skip-verify", "--api-key", "flag-key")
	require.NoError(t, err)

	auth, _, err := sdk.LoadAuth(dir)
	require.NoError(t, err)
	require.NotNil(t, auth)
	assert.Equal(t, "flag-key", auth.APIKey,
		"an explicit flag is a decision; the environment is a fallback")
}

// ---------------------------------------------------------------------------
// What is already on disk
// ---------------------------------------------------------------------------

// TestSetupKeepsTheSettingsItDidNotAskAbout guards the operator who hand-edited
// config.json and then re-ran the onboarding.
//
// setup collects two fields and the file holds more than two. Writing a fresh
// document would silently drop a timeout or a gateway header, and the symptom —
// requests that take longer, or that arrive unattributed — appears in a place
// with no connection to running setup.
func TestSetupKeepsTheSettingsItDidNotAskAbout(t *testing.T) {
	dir := setupTestEnv(t)

	require.NoError(t, sdk.SaveConfig(dir, &sdk.Config{
		Timeout: "4s",
		Headers: map[string]string{"X-Tenant": "acme"},
	}))

	_, _, err := runSetup(t, "", "--yes", "--skip-verify",
		"--api-key", "a-key", "--base-url", "https://example.test/v1", "--model", "m")
	require.NoError(t, err)

	cfg, _, err := sdk.LoadConfig(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "4s", cfg.Timeout)
	assert.Equal(t, map[string]string{"X-Tenant": "acme"}, cfg.Headers)
	assert.Equal(t, "https://example.test/v1", cfg.BaseURL)
	assert.Equal(t, "m", cfg.Model)
}

func TestSetupRefusesABaseURLThatIsNotAbsolute(t *testing.T) {
	dir := setupTestEnv(t)

	// --skip-verify skips the call, not the checks. A base URL with no scheme
	// cannot produce a request, so writing it would leave every later command
	// failing with a transport error blamed on a file nobody just looked at.
	_, _, err := runSetup(t, "", "--yes", "--skip-verify",
		"--api-key", "a-key", "--base-url", "openrouter.ai/api/v1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute")
	assertNoConfiguration(t, dir)
}

// ---------------------------------------------------------------------------
// The questions
// ---------------------------------------------------------------------------

func TestSetupAsksInOrderAndSkipsWhatTheFlagsSupplied(t *testing.T) {
	dir := setupTestEnv(t)

	var out bytes.Buffer
	// The base URL is given, so it is not asked for. The empty line at the key
	// prompt asks again; the model gets an empty line, which takes its default.
	err := setupConfig(context.Background(), &out, strings.NewReader("\nsecret-key\n\n"), true,
		setupFlags{skipVerify: true, baseURL: "https://example.test/v1"})
	require.NoError(t, err, "prompts were: %s", out.String())

	printed := out.String()
	assert.NotContains(t, printed, "Base URL [",
		"a flag the operator typed must not be asked back at them")
	assert.Contains(t, printed, "an API key is required",
		"an empty key is asked for again rather than accepted: SaveAuth refuses it, and "+
			"finding that out after the last question is worse")

	keyAt := strings.Index(printed, "API key:")
	modelAt := strings.Index(printed, "Model [")
	require.GreaterOrEqual(t, keyAt, 0, "the key was never asked for; output was: %s", printed)
	require.GreaterOrEqual(t, modelAt, 0, "the model was never asked for; output was: %s", printed)
	assert.Less(t, keyAt, modelAt,
		"the questions are asked in the order the command documents: base URL, key, model")

	cfg, _, err := sdk.LoadConfig(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "https://example.test/v1", cfg.BaseURL)
	assert.Equal(t, defaultSetupModel, cfg.Model, "an empty answer takes the default")

	auth, _, err := sdk.LoadAuth(dir)
	require.NoError(t, err)
	require.NotNil(t, auth)
	assert.Equal(t, "secret-key", auth.APIKey)
}

func TestSetupWithNoAnswersAtAllTakesTheDefaults(t *testing.T) {
	dir := setupTestEnv(t)

	// End of input is an answer too: the caller closed it, and the defaults are
	// what setup is for. Only the key has no default to fall back on.
	var out bytes.Buffer
	err := setupConfig(context.Background(), &out, strings.NewReader(""), true,
		setupFlags{skipVerify: true, baseURL: "https://example.test/v1", apiKey: "a-key"})
	require.NoError(t, err)

	cfg, _, err := sdk.LoadConfig(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, defaultSetupModel, cfg.Model)
}

func TestSetupEndsWhenTheKeyIsNeverAnswered(t *testing.T) {
	dir := setupTestEnv(t)

	// The default base URL is taken, then the key prompt is answered with an empty
	// line and the input ends. Asking again would loop on the same empty reader
	// forever, which is how a prompt turns into a hang.
	var out bytes.Buffer
	err := setupConfig(context.Background(), &out, strings.NewReader("\n\n"), true, setupFlags{skipVerify: true})

	require.ErrorIs(t, err, errSetupNoAPIKey)
	assertNoConfiguration(t, dir)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// setupTestEnv points setup at a directory it has to create, and clears the key
// variable.
//
// The directory is a subdirectory rather than t.TempDir itself so that the 0700
// assertion is about a directory setup created; t.TempDir already has that mode,
// which would make the check pass whatever SaveConfig did. The key variable is
// cleared so a developer's exported SAEL_API_KEY cannot change a result.
//
// It returns the directory setup will use, which does not exist yet.
func setupTestEnv(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), sdk.ConfigDirName)
	t.Setenv(sdk.EnvConfigDir, dir)
	unsetEnv(t, envSetupAPIKey)

	return dir
}

// fakeSetupService starts a service and returns its URL.
//
// It is fakeService without the environment: setup writes the directory itself,
// and the credential under test comes from the flags, so a variable left over
// from the developer's shell must not be able to reach the request.
func fakeSetupService(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return srv.URL
}

// runSetup runs the whole command tree with setup as the command.
//
// Standard input is a reader rather than a terminal, which is what makes this the
// non-interactive form: the command has to refuse it without --yes, and that
// refusal is a behaviour, not a test artefact.
func runSetup(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	root := newRootCmd()

	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(append([]string{"setup"}, args...))

	err = root.Execute()
	return out.String(), errBuf.String(), err
}

// assertNoConfiguration checks that neither file survived a run that failed.
//
// A configuration left behind after a refused key is worse than no configuration:
// every later command reads it and fails, and the operator is reading an error
// about a file they have no reason to connect to setup.
func assertNoConfiguration(t *testing.T, dir string) {
	t.Helper()

	for _, name := range []string{sdk.ConfigFileName, sdk.AuthFileName} {
		_, err := os.Stat(filepath.Join(dir, name))
		assert.ErrorIs(t, err, fs.ErrNotExist, "%s was written by a run that failed", name)
	}
}

// assertMode checks a file or directory's POSIX mode where the platform has one.
//
// Windows reports a synthesised mode for every file regardless of what was
// requested, so asserting there would test the Go runtime rather than this
// command; the files are still checked for existence and content either way.
func assertMode(t *testing.T, path string, want fs.FileMode) {
	t.Helper()

	if runtime.GOOS == "windows" {
		return
	}

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, want, info.Mode().Perm(),
		"%s is group or world accessible, and one of these files holds the credential", path)
}
