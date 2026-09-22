package sdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Configuration on disk.
//
// Settings live in a directory, by default ~/.Sael, laid out like the other
// agent CLIs:
//
//	~/.Sael/config.json   settings that are safe to read, commit or paste
//	~/.Sael/auth.json     the credential, in its own file
//
// Two files rather than one, so that the half people share is never the half
// that authenticates them. Both are JSON. On Unix the directory is created with
// mode 0700 and both files with 0600; on Windows there are no such bits, so the
// credential relies on the ACL of the user's profile directory. See the
// configDirPerm comment for what that means in practice.
//
// # Reading is explicit
//
// New performs no file I/O. A constructor that silently read a home directory
// would make every caller's behaviour depend on the machine it happens to run
// on, and would make a test suite depend on whoever's laptop is running it. Use
// NewFromConfigDir to have the directory loaded, or load it with LoadConfig and
// LoadAuth and pass the resulting Options yourself.
//
// # Precedence
//
// Lowest to highest: built-in defaults, the files, environment variables, then
// explicit Option values. Code therefore always beats a file on disk, which is
// what makes the file safe to treat as a baseline rather than an override.
const (
	// ConfigDirName is the directory created under the user's home directory.
	ConfigDirName = ".Sael"
	// ConfigFileName holds non-secret settings.
	ConfigFileName = "config.json"
	// AuthFileName holds the credential and nothing else.
	AuthFileName = "auth.json"
)

// EnvConfigDir relocates the configuration directory, which is how tests and
// multi-profile setups stay out of each other's way.
const EnvConfigDir = "SAEL_HOME"

// Permission bits for the configuration files, on the platforms that have them.
//
// Unix enforces these: 0700 for the directory and 0600 for both files, so a
// credential is readable only by its owner. Windows does not. There, Chmod only
// toggles the read-only attribute and FileMode.Perm reports a synthesised
// 0666/0777 whatever was requested, so the file ends up protected by the ACL on
// the user's profile directory and by nothing this package does. The tests do not
// assert the mode on Windows for that reason — asserting it there would be
// testing the Go runtime. Closing the gap properly would mean an ACL dependency,
// which this package does not take; it is documented instead.
const (
	configDirPerm  fs.FileMode = 0o700
	configFilePerm fs.FileMode = 0o600
)

// Config is the contents of config.json.
//
// Every field is optional; an absent field leaves the layer below it in place.
// Durations are strings, because a bare JSON number leaves it ambiguous whether
// 3 means seconds or milliseconds — and a hand-edited file is exactly where that
// ambiguity turns into a bug.
type Config struct {
	// BaseURL is the API root; the client appends "/systemone".
	BaseURL string `json:"base_url,omitempty"`
	// Model is the default model id.
	Model string `json:"model,omitempty"`
	// Timeout bounds a single attempt, for example "10s".
	Timeout string `json:"timeout,omitempty"`
	// TotalTimeout bounds the whole call including retries, for example "3s".
	TotalTimeout string `json:"total_timeout,omitempty"`
	// MaxRetries is a pointer so an explicit 0 disables retries rather than
	// being indistinguishable from an absent field.
	MaxRetries *int `json:"max_retries,omitempty"`
	// MaxResponseBytes bounds how much of a response body is buffered.
	MaxResponseBytes int64 `json:"max_response_bytes,omitempty"`
	// Headers are sent on every request. Authorization, Content-Type and Accept
	// cannot be set here.
	Headers map[string]string `json:"headers,omitempty"`
}

// Auth is the contents of auth.json. It holds the credential and nothing else,
// so that the file which must never be shared is also the file that is trivial
// to read and to rotate.
type Auth struct {
	APIKey string `json:"api_key,omitempty"`
}

// DefaultConfigDir returns $SAEL_HOME when set, otherwise ~/.Sael. It returns ""
// only when neither is available.
func DefaultConfigDir() string {
	if v := envOr(EnvConfigDir); v != "" {
		return expandHome(v)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ConfigDirName)
}

// expandHome resolves a leading ~ so SAEL_HOME can be written the obvious way.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

// Options converts the file settings into Option values, in an order the caller
// can insert wherever their own layering wants.
//
// It returns an error rather than deferring it: a duration that cannot be parsed
// is a fault in a hand-edited file, and reporting it where the file is read is
// far more useful than reporting it later as a mysterious request timeout.
func (c *Config) Options() ([]Option, error) {
	if c == nil {
		return nil, nil
	}
	var opts []Option

	if c.BaseURL != "" {
		opts = append(opts, WithBaseURL(c.BaseURL))
	}
	if c.Model != "" {
		opts = append(opts, WithModel(c.Model))
	}
	if c.Timeout != "" {
		d, err := parseConfigDuration("timeout", c.Timeout)
		if err != nil {
			return nil, err
		}
		opts = append(opts, WithTimeout(d))
	}
	if c.TotalTimeout != "" {
		d, err := parseConfigDuration("total_timeout", c.TotalTimeout)
		if err != nil {
			return nil, err
		}
		opts = append(opts, WithTotalTimeout(d))
	}
	if c.MaxRetries != nil {
		policy := DefaultRetryPolicy()
		policy.MaxRetries = *c.MaxRetries
		opts = append(opts, WithRetryPolicy(policy))
	}
	if c.MaxResponseBytes > 0 {
		opts = append(opts, WithMaxResponseBytes(c.MaxResponseBytes))
	}
	for _, k := range sortedKeys(c.Headers) {
		opts = append(opts, WithHeader(k, c.Headers[k]))
	}
	return opts, nil
}

// Options converts the stored credential into an Option.
func (a *Auth) Options() []Option {
	if a == nil || a.APIKey == "" {
		return nil
	}
	return []Option{WithAPIKey(a.APIKey)}
}

// sortedKeys keeps option order deterministic. Header order does not matter on
// the wire, but a stable order makes failures reproducible.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// LoadConfig reads dir/config.json.
//
// A missing file is not an error: it returns (nil, false, nil), which is the
// ordinary state on a machine that has never been configured. A file that exists
// but cannot be parsed is an error, because silently ignoring a typo in a
// hand-edited file is how people lose an afternoon.
func LoadConfig(dir string) (*Config, bool, error) {
	var cfg Config
	found, err := loadJSON(dir, ConfigFileName, &cfg)
	if err != nil || !found {
		return nil, false, err
	}
	return &cfg, true, nil
}

// LoadAuth reads dir/auth.json. Missing and unparsable are treated the same way
// as in LoadConfig.
func LoadAuth(dir string) (*Auth, bool, error) {
	var auth Auth
	found, err := loadJSON(dir, AuthFileName, &auth)
	if err != nil || !found {
		return nil, false, err
	}
	return &auth, true, nil
}

func loadJSON(dir, name string, into any) (bool, error) {
	if dir == "" {
		return false, nil
	}
	path := filepath.Join(dir, name)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: reading %s: %w", ErrConfig, path, err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return false, fmt.Errorf("%w: parsing %s: %w", ErrConfig, path, err)
	}
	return true, nil
}

// SaveConfig writes dir/config.json, creating the directory if needed.
func SaveConfig(dir string, cfg *Config) error {
	return saveJSON(dir, ConfigFileName, cfg)
}

// SaveAuth writes dir/auth.json with mode 0600, creating the directory if needed.
// The mode is enforced on Unix only; see the configDirPerm comment.
//
// The credential is written unencrypted, which is the same bargain ssh and the
// other agent CLIs make: the protection is the file's mode where the platform has
// one, and the user's profile directory everywhere, not a password.
func SaveAuth(dir string, auth *Auth) error {
	if auth == nil || strings.TrimSpace(auth.APIKey) == "" {
		return fmt.Errorf("%w: refusing to write an empty API key", ErrConfig)
	}
	auth.APIKey = strings.TrimSpace(auth.APIKey)
	return saveJSON(dir, AuthFileName, auth)
}

func saveJSON(dir, name string, v any) error {
	if dir == "" {
		return fmt.Errorf("%w: no configuration directory", ErrConfig)
	}
	if err := os.MkdirAll(dir, configDirPerm); err != nil {
		return fmt.Errorf("%w: creating %s: %w", ErrConfig, dir, err)
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: encoding %s: %w", ErrConfig, name, err)
	}
	raw = append(raw, '\n')
	return writeFileAtomic(filepath.Join(dir, name), raw, configFilePerm)
}

// writeFileAtomic writes via a temporary file in the same directory and renames
// it into place, so a crash partway through cannot leave a truncated file.
func writeFileAtomic(path string, data []byte, perm fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("%w: creating a temporary file in %s: %w", ErrConfig, filepath.Dir(path), err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Best effort: on the success path the rename has already consumed it.
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("%w: setting mode on %s: %w", ErrConfig, tmpName, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("%w: writing %s: %w", ErrConfig, tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("%w: closing %s: %w", ErrConfig, tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("%w: replacing %s: %w", ErrConfig, path, err)
	}
	return nil
}

func parseConfigDuration(field, value string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%w: %s: %q is not a duration such as \"3s\" or \"500ms\": %w", ErrConfig, field, value, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("%w: %s must not be negative, got %q", ErrConfig, field, value)
	}
	return d, nil
}

// NewFromConfigDir builds a client whose settings come from dir, then from the
// environment, then from opts.
//
// An empty dir means DefaultConfigDir, so a command-line tool starts with
// sdk.NewFromConfigDir("") and needs no path handling of its own. A
// missing directory or missing files are not errors; a file that exists and does
// not parse is.
func NewFromConfigDir(dir string, opts ...Option) (*Client, error) {
	if dir == "" {
		dir = DefaultConfigDir()
	}

	fileCfg, _, err := LoadConfig(dir)
	if err != nil {
		return nil, err
	}
	fileOpts, err := fileCfg.Options()
	if err != nil {
		return nil, err
	}

	auth, _, err := LoadAuth(dir)
	if err != nil {
		return nil, err
	}
	fileOpts = append(fileOpts, auth.Options()...)

	return newClient(fileOpts, opts)
}
