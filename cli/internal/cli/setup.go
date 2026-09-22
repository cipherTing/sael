package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cipherTing/sael/sdk"
)

// defaultSetupModel is the model the prompt suggests.
//
// It is not sdk.DefaultModel: that is the SDK's neutral "jev-latest", while the
// onboarding has to suggest a model id the operator can recognise in a receipt.
// The value is pinned here because it is a deployment's choice, not a fact about
// the API.
const defaultSetupModel = "jev-1.13.0"

// envSetupAPIKey is where a caller with no terminal puts the key it was given.
//
// It is deliberately not the SDK's TYPESAFE_API_KEY. That variable is the
// runtime override, and reading it here would persist whatever happened to be
// exported for one command into a file the operator did not ask to change.
const envSetupAPIKey = "SAEL_API_KEY"

var (
	// errSetupNotInteractive refuses to ask questions that have nowhere to go.
	//
	// Reading a prompt from a pipe does not block the way reading a terminal
	// does: the input ends, every answer comes back empty, and setup writes a
	// configuration nobody chose. In an install script that is the failure that
	// is hardest to see, so it is refused with the alternative in the message.
	errSetupNotInteractive = errors.New(
		"sael setup asks questions and standard input is not a terminal: run it from " +
			"a terminal, or pass --yes to take the defaults and the flags given")

	// errSetupNoAPIKey is the one answer with no default to fall back on. Writing
	// the file anyway is not an option: SaveAuth refuses an empty credential, and
	// a config that is missing its key fails later with a message about a file
	// rather than about the missing answer.
	errSetupNoAPIKey = errors.New("no API key: pass --api-key or set " + envSetupAPIKey)
)

// setupFlags are the answers given on the command line, before anything is asked.
type setupFlags struct {
	yes        bool
	skipVerify bool
	baseURL    string
	model      string
	apiKey     string
}

// setupSettings is what setup decided to write.
type setupSettings struct {
	baseURL string
	apiKey  string
	model   string
}

func newSetupCmd() *cobra.Command {
	var flags setupFlags

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Ask for the service URL, API key and model, and write them",
		Long: `Configure sael for this machine.

The base URL, the API key and the model are asked for in that order, each with a
default except the key. The answers go to the same two files every other command
reads, written by the SDK rather than by this command, so the format and the file
modes have one implementation.

Before anything is written the key is used for one real request. A key that the
service refuses, or a host that cannot be reached, is reported while the operator
is still in front of the machine — and nothing is written, so a broken
configuration cannot be left behind for the next command to trip over. Use
--skip-verify to write the files without that call.

With --yes the questions are not asked at all: the defaults and whatever the flags
and the environment supplied are written straight away, which is the form an
install script runs.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return setupConfig(cmd.Context(), cmd.OutOrStdout(), cmd.InOrStdin(), stdinIsTerminal(), flags)
		},
	}

	cmd.Flags().BoolVar(&flags.yes, "yes", false,
		"take the defaults and the values given here, without asking anything")
	cmd.Flags().BoolVar(&flags.skipVerify, "skip-verify", false,
		"write the configuration without checking the key against the service")
	cmd.Flags().StringVar(&flags.baseURL, "base-url", "",
		"API root to write instead of "+sdk.DefaultBaseURL)
	cmd.Flags().StringVar(&flags.model, "model", "",
		"model id to write instead of "+defaultSetupModel)
	cmd.Flags().StringVar(&flags.apiKey, "api-key", "",
		"API key to write instead of reading "+envSetupAPIKey)

	return cmd
}

// setupConfig is the whole command, with the streams and the answer to "is
// standard input a terminal" passed in.
//
// Both are arguments rather than detected here for the same reason readTextFrom
// takes its terminal flag: a test process has no terminal to offer, and the
// refusal below is the branch that most needs to be reachable, because hanging or
// writing defaults is how a setup command fails inside an install script.
func setupConfig(ctx context.Context, out io.Writer, in io.Reader, terminal bool, flags setupFlags) error {
	ask := !flags.yes
	if ask && !terminal {
		return errSetupNotInteractive
	}

	settings, err := resolveSetupSettings(in, out, ask, flags)
	if err != nil {
		return err
	}

	// An empty directory means the default location, so setup and check agree on
	// where the files are without either of them spelling out a path.
	dir := sdk.DefaultConfigDir()
	if dir == "" {
		return fmt.Errorf("no configuration directory: set %s or HOME", sdk.EnvConfigDir)
	}

	// The client is built even when the call is skipped, because construction is
	// where the SDK rejects a base URL that is not absolute and a key it cannot
	// use. Those checks cost nothing and catch a typo that would otherwise be
	// written to disk and reported by the next command as a transport failure.
	client, err := sdk.New(
		sdk.WithBaseURL(settings.baseURL),
		sdk.WithModel(settings.model),
		sdk.WithAPIKey(settings.apiKey),
		// A probe is a diagnosis, not a delivery. The default policy retries a
		// failed connection twice, which cannot change the answer and delays the
		// one thing the operator is waiting for, so it is turned off for this call.
		sdk.WithRetryPolicy(sdk.RetryPolicy{}),
	)
	if err != nil {
		return err
	}

	if !flags.skipVerify {
		if err := probeService(ctx, client); err != nil {
			return setupProbeError(err, settings.baseURL)
		}
	}

	cfg, _, err := sdk.LoadConfig(dir)
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = &sdk.Config{}
	}
	// Only the two fields setup asked about are replaced. An operator who
	// hand-set a timeout or a gateway header and re-runs the onboarding must not
	// lose it, and a file that does not parse is reported here rather than
	// replaced by the answers just collected.
	cfg.BaseURL = settings.baseURL
	cfg.Model = settings.model

	if err := sdk.SaveConfig(dir, cfg); err != nil {
		return err
	}
	// Written second, so a failure partway through leaves the state that re-running
	// setup repairs: settings without a credential, rather than a credential
	// nobody can find because the file naming it was never created.
	if err := sdk.SaveAuth(dir, &sdk.Auth{APIKey: settings.apiKey}); err != nil {
		return err
	}

	// The install script shows these paths, so they are printed rather than
	// summarised. "Configuration written" would leave the operator guessing which
	// directory, and SAEL_HOME is exactly what makes that non-obvious.
	_, err = fmt.Fprintf(out, "configuration written to %s\nAPI key written to %s\n",
		filepath.Join(dir, sdk.ConfigFileName), filepath.Join(dir, sdk.AuthFileName))
	return err
}

// resolveSetupSettings fills in the three answers, asking for whatever the
// command line did not supply.
//
// The order of the questions is the order of the fields, and a value given as a
// flag is not asked for again: an operator who typed --base-url has answered that
// question already, and asking it back reads as the flag having been ignored.
func resolveSetupSettings(in io.Reader, out io.Writer, ask bool, flags setupFlags) (setupSettings, error) {
	settings := setupSettings{
		baseURL: strings.TrimSpace(flags.baseURL),
		model:   strings.TrimSpace(flags.model),
		apiKey:  strings.TrimSpace(flags.apiKey),
	}

	// The environment supplies the key when the flag did not. An install script
	// holds the credential in a variable rather than in the command line, which a
	// process listing would expose.
	if settings.apiKey == "" {
		settings.apiKey = strings.TrimSpace(os.Getenv(envSetupAPIKey))
	}

	if ask {
		reader := bufio.NewReader(in)

		if settings.baseURL == "" {
			answer, err := askSetupLine(reader, out, "Base URL", sdk.DefaultBaseURL)
			if err != nil {
				return setupSettings{}, err
			}
			settings.baseURL = answer
		}

		if settings.apiKey == "" {
			answer, err := askSetupAPIKey(reader, out)
			if err != nil {
				return setupSettings{}, err
			}
			settings.apiKey = answer
		}

		if settings.model == "" {
			answer, err := askSetupLine(reader, out, "Model", defaultSetupModel)
			if err != nil {
				return setupSettings{}, err
			}
			settings.model = answer
		}
	}

	if settings.baseURL == "" {
		settings.baseURL = sdk.DefaultBaseURL
	}
	if settings.model == "" {
		settings.model = defaultSetupModel
	}
	if settings.apiKey == "" {
		return setupSettings{}, errSetupNoAPIKey
	}

	return settings, nil
}

// askSetupLine writes one question with its default and reads one answer.
//
// An empty line takes the default, and so does the end of the input: a caller who
// closed the input answered nothing, and for these two questions that is what the
// default is for. The errors that are not end-of-input are returned rather than
// treated as an empty answer, because a closed terminal and a broken pipe both
// mean every question after this one would collect nonsense.
func askSetupLine(r *bufio.Reader, w io.Writer, label, def string) (string, error) {
	if _, err := fmt.Fprintf(w, "%s [%s]: ", label, def); err != nil {
		return "", err
	}

	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("reading the answer to %s: %w", label, err)
	}
	if answer := strings.TrimSpace(line); answer != "" {
		return answer, nil
	}
	return def, nil
}

// askSetupAPIKey reads the key, which has no default to fall back on.
//
// An empty answer is asked again rather than accepted: discovering at the end of
// the questions that the key was empty is a worse way to learn it than being
// asked once more. The end of the input is not a retry, it is a refusal — with
// nothing left to read, asking again would loop forever on the same empty line.
func askSetupAPIKey(r *bufio.Reader, w io.Writer) (string, error) {
	for {
		if _, err := fmt.Fprint(w, "API key: "); err != nil {
			return "", err
		}

		line, err := r.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("reading the API key: %w", err)
		}
		if key := strings.TrimSpace(line); key != "" {
			return key, nil
		}
		if errors.Is(err, io.EOF) {
			return "", errSetupNoAPIKey
		}

		if _, err := fmt.Fprintln(w, "an API key is required"); err != nil {
			return "", err
		}
	}
}

// probeService makes the one call that proves the credential and the host.
//
// It is the smallest request the service accepts: one yes/no question with a
// string instruction and no criteria, which is the shape captured from the live
// endpoint in sdk/testdata/golden/noul_string.json. A probe is a real, billed
// call like any other, so it stays as small as it can be while still exercising
// the same path a check takes.
func probeService(ctx context.Context, client *sdk.Client) error {
	const state = "ping"

	probe := sdk.Questions{
		"probe": sdk.NoulQuestion{Instructions: "Is this text empty?"},
	}

	_, err := client.Evaluate(ctx, state, probe)
	return err
}

// setupProbeError turns a failed probe into the message that tells the operator
// what to do next.
//
// The SDK's error already carries what the service said or which host refused the
// connection; what it cannot say is that nothing was written. That is the fact
// that matters most here: a configuration left behind after a failed check would
// break every later command, and the operator would be reading a 401 about a file
// they did not know existed.
func setupProbeError(err error, baseURL string) error {
	const untouched = "nothing was written: check the key and --base-url, or pass " +
		"--skip-verify to write the configuration anyway"

	switch {
	case errors.Is(err, sdk.ErrRateLimit):
		return fmt.Errorf("%s is rate limiting this key: %w\n%s", baseURL, err, untouched)
	case errors.Is(err, sdk.ErrAPI):
		return fmt.Errorf("%s refused the API key: %w\n%s", baseURL, err, untouched)
	case errors.Is(err, sdk.ErrConnection):
		return fmt.Errorf("could not reach %s: %w\n%s", baseURL, err, untouched)
	case errors.Is(err, sdk.ErrTimeout):
		return fmt.Errorf("%s did not answer in time: %w\n%s", baseURL, err, untouched)
	default:
		return fmt.Errorf("the API key could not be verified against %s: %w\n%s",
			baseURL, err, untouched)
	}
}
