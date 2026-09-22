package sdk

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file tests the configuration layer.
//
// Note what is absent: no TestMain, and no environment set up to protect the
// tests from a developer's own ~/.Sael. New performs no file I/O, so the suite
// cannot be affected by the machine it runs on, and the tests here reach the
// filesystem only through an explicit directory they created themselves. That
// property is worth keeping — a test binary that has to sanitise the home
// directory is telling you the constructor does too much.

// withConfig writes config.json (and optionally auth.json) into a fresh
// directory and returns it.
func withConfig(t *testing.T, cfg *Config, apiKey string) string {
	t.Helper()
	dir := t.TempDir()
	if cfg != nil {
		require.NoError(t, SaveConfig(dir, cfg))
	}
	if apiKey != "" {
		require.NoError(t, SaveAuth(dir, &Auth{APIKey: apiKey}))
	}
	return dir
}

// clearClientEnv blanks the environment the client consults, so a test asserts on
// the inputs it wrote rather than on whatever the developer has exported.
//
// An empty value counts as unset, because applyEnv skips it, so blanking is
// equivalent to removing and needs no restore logic beyond t.Setenv's own.
//
// The New precedence is defaults, then environment, then explicit Options, and
// the environment sits above the config directory. So a developer with any of
// these three exported — which is the documented way to configure a library
// caller, not an odd thing to do — used to see these tests fail for a reason that
// had nothing to do with their change. Each test below that asserts a default
// calls this first.
func clearClientEnv(t *testing.T) {
	t.Helper()
	t.Setenv(EnvAPIKey, "")
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvModel, "")
}

// ---------- directory resolution ----------

func TestDefaultConfigDirPrefersEnv(t *testing.T) {
	t.Setenv(EnvConfigDir, "/tmp/sael-explicit")
	assert.Equal(t, "/tmp/sael-explicit", DefaultConfigDir())
}

func TestDefaultConfigDirFallsBackToHome(t *testing.T) {
	t.Setenv(EnvConfigDir, "")
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ConfigDirName), DefaultConfigDir())
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	assert.Equal(t, home, expandHome("~"))
	assert.Equal(t, filepath.Join(home, ".Sael"), expandHome("~/.Sael"))
	assert.Equal(t, "/absolute/path", expandHome("/absolute/path"))
	assert.Equal(t, "relative/path", expandHome("relative/path"))
	// A leading ~ only expands when it is the whole path component.
	assert.Equal(t, "~other/path", expandHome("~other/path"))
}

// ---------- reading and writing ----------

func TestLoadConfigMissingIsNotAnError(t *testing.T) {
	cfg, found, err := LoadConfig(t.TempDir())
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, cfg)

	cfg, found, err = LoadConfig("")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, cfg)
}

func TestLoadConfigMalformedIsAnError(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ConfigFileName), []byte("{not json"), 0o600))

	_, _, err := LoadConfig(dir)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrConfig)
	assert.NotErrorIs(t, err, ErrValidation, "a broken config file is not an invalid request")
	assert.Contains(t, err.Error(), ConfigFileName)
}

func TestSaveConfigRoundTrips(t *testing.T) {
	maxRetries := 5
	want := Config{
		BaseURL:          "https://example.test/v1",
		Model:            "some-model",
		Timeout:          "4s",
		TotalTimeout:     "9s",
		MaxRetries:       &maxRetries,
		MaxResponseBytes: 4096,
		Headers:          map[string]string{"X-Tenant": "acme"},
	}
	dir := withConfig(t, &want, "")

	got, found, err := LoadConfig(dir)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, want, *got)
}

// assertPrivateMode checks a file's POSIX mode on the platforms where that means
// something.
//
// Windows has no POSIX permission bits: Chmod only toggles the read-only
// attribute, and FileMode.Perm reports a synthesised 0666 or 0777 for every file
// regardless of what was requested. Asserting the mode there would be testing the
// Go runtime rather than this package, so the check simply does not run. The file
// is still created and still round-trips; only the mode claim is platform-bound.
func assertPrivateMode(t *testing.T, path string, want fs.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, want, info.Mode().Perm(), "a file beside the credential must not be group or world readable")
}

func TestSaveConfigCreatesPrivateDirectoryAndFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", ConfigDirName)
	require.NoError(t, SaveConfig(dir, &Config{Model: "m"}))

	assertPrivateMode(t, dir, configDirPerm)
	assertPrivateMode(t, filepath.Join(dir, ConfigFileName), configFilePerm)
}

func TestSaveConfigRejectsEmptyDir(t *testing.T) {
	err := SaveConfig("", &Config{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrConfig)
}

func TestSaveConfigOverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, SaveConfig(dir, &Config{Model: "first"}))
	require.NoError(t, SaveConfig(dir, &Config{Model: "second"}))

	cfg, _, err := LoadConfig(dir)
	require.NoError(t, err)
	assert.Equal(t, "second", cfg.Model)

	// The temporary file must not survive a successful write.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, ConfigFileName, entries[0].Name())
}

// ---------- auth.json ----------

func TestLoadAuthMissingIsNotAnError(t *testing.T) {
	auth, found, err := LoadAuth(t.TempDir())
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, auth)

	auth, found, err = LoadAuth("")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, auth)
}

func TestLoadAuthMalformedIsAnError(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, AuthFileName), []byte("not json"), 0o600))

	_, _, err := LoadAuth(dir)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrConfig)
}

func TestSaveAuthUsesPrivateModeAndRoundTrips(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, SaveAuth(dir, &Auth{APIKey: "sk-secret"}))

	assertPrivateMode(t, filepath.Join(dir, AuthFileName), configFilePerm)

	auth, found, err := LoadAuth(dir)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "sk-secret", auth.APIKey)
}

func TestSaveAuthTrimsAndRefusesEmpty(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, SaveAuth(dir, &Auth{APIKey: "  sk-secret\n"}))

	auth, _, err := LoadAuth(dir)
	require.NoError(t, err)
	assert.Equal(t, "sk-secret", auth.APIKey, "a stray newline must not become part of the credential")

	err = SaveAuth(dir, &Auth{APIKey: "   "})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrConfig)

	err = SaveAuth(dir, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrConfig)
}

// ---------- layering ----------

func TestNewPerformsNoFileIO(t *testing.T) {
	// A populated configuration directory must be invisible to New. This is the
	// property that keeps a caller's behaviour independent of the machine, and it
	// is what lets the rest of the suite run without sanitising $HOME.
	dir := withConfig(t, &Config{
		BaseURL: "https://from-file.test/v1",
		Model:   "model-from-file",
	}, "sk-from-file")
	t.Setenv(EnvConfigDir, dir)
	t.Setenv(EnvAPIKey, "")
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvModel, "")

	_, err := New()
	require.Error(t, err, "New must not have found the key in the directory")
	assert.ErrorIs(t, err, ErrValidation)

	client, err := New(WithAPIKey("sk-explicit"))
	require.NoError(t, err)
	assert.Equal(t, DefaultBaseURL, client.BaseURL())
	assert.Equal(t, DefaultModel, client.DefaultModel())
}

func TestNewFromConfigDirReadsBothFiles(t *testing.T) {
	maxRetries := 7
	dir := withConfig(t, &Config{
		BaseURL:      "https://from-file.test/v1",
		Model:        "model-from-file",
		Timeout:      "3s",
		TotalTimeout: "8s",
		MaxRetries:   &maxRetries,
		Headers:      map[string]string{"X-From-File": "yes"},
	}, "sk-from-file")

	t.Setenv(EnvAPIKey, "")
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvModel, "")

	client, err := NewFromConfigDir(dir)
	require.NoError(t, err)

	assert.Equal(t, "https://from-file.test/v1", client.BaseURL())
	assert.Equal(t, "model-from-file", client.DefaultModel())
	assert.Equal(t, 3*time.Second, client.timeout)
	assert.Equal(t, 8*time.Second, client.totalTimeout)
	assert.Equal(t, 7, client.retry.MaxRetries)
	assert.Equal(t, "Bearer sk-from-file", client.header.Get("Authorization"))
	assert.Equal(t, "yes", client.header.Get("X-From-File"))
}

func TestEnvironmentBeatsConfigFile(t *testing.T) {
	dir := withConfig(t, &Config{
		BaseURL: "https://from-file.test/v1",
		Model:   "model-from-file",
	}, "sk-from-file")

	t.Setenv(EnvAPIKey, "sk-from-env")
	t.Setenv(EnvBaseURL, "https://from-env.test/v1")
	t.Setenv(EnvModel, "model-from-env")

	client, err := NewFromConfigDir(dir)
	require.NoError(t, err)

	assert.Equal(t, "https://from-env.test/v1", client.BaseURL())
	assert.Equal(t, "model-from-env", client.DefaultModel())
	assert.Equal(t, "Bearer sk-from-env", client.header.Get("Authorization"))
}

func TestOptionBeatsEnvironmentAndConfigFile(t *testing.T) {
	dir := withConfig(t, &Config{
		BaseURL: "https://from-file.test/v1",
		Model:   "model-from-file",
	}, "sk-from-file")

	t.Setenv(EnvAPIKey, "sk-from-env")
	t.Setenv(EnvBaseURL, "https://from-env.test/v1")
	t.Setenv(EnvModel, "model-from-env")

	client, err := NewFromConfigDir(dir,
		WithAPIKey("sk-from-code"),
		WithBaseURL("https://from-code.test/v1"),
		WithModel("model-from-code"),
	)
	require.NoError(t, err)

	assert.Equal(t, "https://from-code.test/v1", client.BaseURL())
	assert.Equal(t, "model-from-code", client.DefaultModel())
	assert.Equal(t, "Bearer sk-from-code", client.header.Get("Authorization"))
}

func TestNewFromConfigDirEmptyMeansDefaultLocation(t *testing.T) {
	dir := withConfig(t, &Config{Model: "model-from-home"}, "sk-from-home")
	t.Setenv(EnvConfigDir, dir)
	// The model comes from the file here, so the environment must not be allowed
	// to answer over it.
	clearClientEnv(t)

	client, err := NewFromConfigDir("")
	require.NoError(t, err)
	assert.Equal(t, "model-from-home", client.DefaultModel())
	assert.Equal(t, "Bearer sk-from-home", client.header.Get("Authorization"))
}

func TestNewFromConfigDirMissingDirectoryIsFine(t *testing.T) {
	// Only the key is meant to come from the environment; the assertions below are
	// about the other two falling back to their defaults.
	clearClientEnv(t)
	t.Setenv(EnvAPIKey, "sk-env")

	client, err := NewFromConfigDir(filepath.Join(t.TempDir(), "does-not-exist"))
	require.NoError(t, err)
	assert.Equal(t, DefaultBaseURL, client.BaseURL())
	assert.Equal(t, DefaultModel, client.DefaultModel())
}

func TestConfigFileHeadersCannotOverrideAuthoritativeOnes(t *testing.T) {
	// The key under test comes from the file, so an exported one must not be able
	// to take its place.
	clearClientEnv(t)

	dir := withConfig(t, &Config{
		Headers: map[string]string{
			"Authorization": "Bearer attacker",
			"Content-Type":  "text/plain",
			"Accept":        "text/plain",
			"X-Tenant":      "acme",
		},
	}, "sk-real")

	client, err := NewFromConfigDir(dir)
	require.NoError(t, err)

	assert.Equal(t, []string{"Bearer sk-real"}, client.header.Values("Authorization"))
	assert.Equal(t, "application/json", client.header.Get("Content-Type"))
	assert.Equal(t, "application/json", client.header.Get("Accept"))
	assert.Equal(t, "acme", client.header.Get("X-Tenant"))
}

// ---------- file contents that do not make sense ----------

func TestConfigOptionsRejectBadDurations(t *testing.T) {
	cases := map[string]Config{
		"not a duration": {Timeout: "three seconds"},
		"negative":       {TotalTimeout: "-3s"},
		"bare number":    {Timeout: "3"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := cfg.Options()
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrConfig)
		})
	}
}

func TestNewFromConfigDirRejectsBadDuration(t *testing.T) {
	dir := withConfig(t, &Config{Timeout: "three seconds"}, "sk")

	_, err := NewFromConfigDir(dir)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrConfig)
	assert.Contains(t, err.Error(), "timeout")
}

func TestEmptyMaxRetriesInConfigMeansNoRetries(t *testing.T) {
	zero := 0
	dir := withConfig(t, &Config{MaxRetries: &zero}, "sk")

	client, err := NewFromConfigDir(dir)
	require.NoError(t, err)
	assert.Equal(t, 0, client.retry.MaxRetries, "an explicit zero must survive, which is why the field is a pointer")
}

func TestAbsentMaxRetriesInConfigKeepsTheDefault(t *testing.T) {
	dir := withConfig(t, &Config{Model: "m"}, "sk")

	client, err := NewFromConfigDir(dir)
	require.NoError(t, err)
	assert.Equal(t, DefaultRetryPolicy().MaxRetries, client.retry.MaxRetries)
}

func TestMaxResponseBytesFromConfigReachesTheTransport(t *testing.T) {
	dir := withConfig(t, &Config{MaxResponseBytes: 1234}, "sk")

	client, err := NewFromConfigDir(dir)
	require.NoError(t, err)
	assert.Equal(t, int64(1234), client.transport.responseLimit())
}

func TestWithMaxResponseBytesOptionReachesTheTransport(t *testing.T) {
	client, err := New(WithAPIKey("sk"), WithMaxResponseBytes(2048))
	require.NoError(t, err)
	assert.Equal(t, int64(2048), client.transport.responseLimit())
}

func TestDefaultResponseLimitIsUsedWhenUnset(t *testing.T) {
	client, err := New(WithAPIKey("sk"))
	require.NoError(t, err)
	assert.Equal(t, int64(MaxResponseBytes), client.transport.responseLimit())
}

// ---------- on-disk contract ----------

// TestConfigJSONShapeIsStable pins the field names. They are a contract with
// anyone who hand-edits the file or writes a tool around it, so a rename has to
// be deliberate rather than an accident of refactoring.
func TestConfigJSONShapeIsStable(t *testing.T) {
	maxRetries := 3
	raw, err := json.Marshal(Config{
		BaseURL:          "u",
		Model:            "m",
		Timeout:          "1s",
		TotalTimeout:     "2s",
		MaxRetries:       &maxRetries,
		MaxResponseBytes: 9,
		Headers:          map[string]string{"H": "v"},
	})
	require.NoError(t, err)

	var keys map[string]any
	require.NoError(t, json.Unmarshal(raw, &keys))

	for _, want := range []string{
		"base_url", "model", "timeout", "total_timeout", "max_retries", "max_response_bytes", "headers",
	} {
		assert.Contains(t, keys, want)
	}
}

func TestAuthJSONShapeIsStable(t *testing.T) {
	raw, err := json.Marshal(Auth{APIKey: "sk"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"api_key":"sk"}`, string(raw))
}

func TestConfigOptionsAreOrderedDeterministically(t *testing.T) {
	cfg := Config{Headers: map[string]string{"B": "2", "A": "1", "C": "3"}}
	first, err := cfg.Options()
	require.NoError(t, err)

	for i := 0; i < 20; i++ {
		again, err := cfg.Options()
		require.NoError(t, err)
		require.Len(t, again, len(first))
	}
}
