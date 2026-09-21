# sael

[![CI](https://github.com/cipherTing/sael/actions/workflows/ci.yml/badge.svg)](https://github.com/cipherTing/sael/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/cipherTing/sael/systemone.svg)](https://pkg.go.dev/github.com/cipherTing/sael/systemone)
[![Go Report Card](https://goreportcard.com/badge/github.com/cipherTing/sael)](https://goreportcard.com/report/github.com/cipherTing/sael)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A Go client for the TypeSafe **System One** API — the Jev model family — and the
first piece of **sael**, a content-safety classifier for an AI request relay.

Jev does not generate text. You send a state (the material to judge) and a set of
typed questions; it returns typed answers with probabilities. That shape is a
good fit for moderation and guardrails, where you want a decision your code can
branch on rather than a paragraph to parse.

## Install

```sh
go get github.com/cipherTing/sael/systemone
```

Requires Go 1.23 or newer.

## Quick start

```go
client, err := systemone.New(
	systemone.WithAPIKey(os.Getenv(systemone.EnvAPIKey)),
	systemone.WithTotalTimeout(3*time.Second),
)
if err != nil {
	return err
}

result, err := client.Evaluate(ctx, userMessage, systemone.Questions{
	"harmful_request": systemone.NoulQuestion{
		Instructions: "Does this ask for help harming someone, or for help breaking the law?",
	},
	"severity": systemone.ScoreQuestion{
		Instructions: "If this request were complied with, how much harm would it do?",
		Criteria: []systemone.Entry{
			"None: an ordinary request.",
			"Mild: sensitive, but complying does no real damage.",
			"Serious: complying enables real wrongdoing.",
			"Severe: complying causes serious harm.",
		},
	},
})
if err != nil {
	return err
}

harmful, _ := result.Noul("harmful_request")
severity, _ := result.Score("severity")
```

One call carries every question: they are evaluated in parallel against the same
state, so adding questions costs a few tokens rather than another round trip.
Ask several narrow questions and combine them in your own code, where the
thresholds belong.

Runnable examples live in [example_test.go](systemone/example_test.go) and appear
in the [package documentation](https://pkg.go.dev/github.com/cipherTing/sael/systemone).

## Configuration

Settings live in `~/.Sael`, laid out like the other agent CLIs:

```
~/.Sael/config.json    settings that are safe to read, commit or paste
~/.Sael/auth.json      the credential, in its own file, mode 0600
```

Two files rather than one, so that the half people share is never the half that
authenticates them. `SaveConfig` and `SaveAuth` create the directory with mode
0700 and write through a temporary file.

```json
// config.json
{
  "base_url": "https://api.typesafe.ai/v1",
  "model": "jev-latest",
  "timeout": "10s",
  "total_timeout": "3s",
  "max_retries": 2
}
```

```json
// auth.json
{ "api_key": "..." }
```

Durations are strings such as `"3s"` or `"500ms"`: a bare JSON number leaves it
ambiguous whether `3` means seconds or milliseconds.

Read it with `NewFromConfigDir`, which takes an empty directory to mean
`DefaultConfigDir()` — so a command-line tool needs no path handling of its own:

```go
client, err := systemone.NewFromConfigDir("")
```

**`New` performs no file I/O.** A constructor that silently read a home directory
would make every caller's behaviour depend on the machine it happens to run on,
and would make a test suite depend on whoever's laptop is running it.

### Precedence

Lowest to highest: built-in defaults, then the files, then the environment
(`TYPESAFE_BASE_URL`, `TYPESAFE_DEFAULT_MODEL`, `TYPESAFE_API_KEY`), then
explicit `Option` values. Code always beats a file on disk, which is what makes
the file safe to treat as a baseline rather than an override.

## The three primitives

| Question | Goal | Answer |
| --- | --- | --- |
| `Noul` | Is this statement true? | `Noul` — a probability in `[0,1]` |
| `Choice` | Pick one option | `Choice`, `Probabilities`, `Confidence` |
| `Score` | Place the state on a rubric | `Score`, `Legend`, `Probabilities`, `Confidence` |

They can be mixed in one request. Read answers with `Result.Noul`,
`Result.Choice` and `Result.Score`, each of which reports whether the name
existed and carried the expected type — a name mismatch is a `false`, not a
panic.

Two details in a `Score` answer are worth knowing before they surprise you:
`Legend` and `Probabilities` are indexed by score, and `Score` is an expectation
that may fall between levels, so it is not an index into `Legend`.

## Errors

Every error matches exactly one sentinel, so one `errors.Is` switch is enough to
decide between failing the user's request, failing over, or failing open.

```go
switch {
case errors.Is(err, systemone.ErrRateLimit):
	var rate *systemone.RateLimitError
	errors.As(err, &rate) // rate.RetryAfter
case errors.Is(err, systemone.ErrAPI):        // any other non-2xx
case errors.Is(err, systemone.ErrTimeout):    // deadline passed
case errors.Is(err, systemone.ErrConnection): // never reached the host
case errors.Is(err, systemone.ErrAborted):    // the caller cancelled
case errors.Is(err, systemone.ErrDecode):     // 2xx body unusable
case errors.Is(err, systemone.ErrValidation): // rejected before sending
case errors.Is(err, systemone.ErrConfig):     // the configuration is unusable
}
```

`ErrConfig` is deliberately distinct from `ErrValidation`: a caller that falls
back when a question set is rejected must not also swallow a broken
`config.json`.

## Vendor neutrality

No host is baked in. The client speaks the API shape and nothing more, so
`BaseURL` can point at the official host, at a gateway, at a proxy, or at a local
test server, with no other change. A model id namespaced for a gateway is just a
string. Gateways may return fields the official schema does not list; those
decode when present and stay zero when absent.

## Testing

```sh
make test              # offline suite
make test-race         # offline suite under the race detector
make test-integration  # the live tests; needs TYPESAFE_API_KEY
make check             # everything CI runs except the live tests
make help              # list every target
```

The offline suite is the default and needs no network: unit tests, replay of
responses captured from a live endpoint ([testdata/golden](systemone/testdata/golden)),
and hostile-input handling. The live tests are gated on `TYPESAFE_API_KEY` and
read `TYPESAFE_BASE_URL`, so they can be pointed at any host that speaks the API.

## Status and known limits

This is early software. The following are known and are not bugs waiting to be
found:

- **The live tests have only ever been run through one gateway.** The behaviour
  the client is written against was observed going through a gateway, not against
  the official host directly. Where the two disagreed, this package follows the
  documented reference and says so at the point of divergence, in
  [doc.go](systemone/doc.go).
- **Retry, timeout, redirect and rate-limit handling are covered only by
  `httptest`.** No real network failure mode has been exercised.
- **The package makes no judgement about whether an answer is correct.** Tests
  assert structural invariants — probabilities in range, distributions summing to
  one, a `Legend` matching the criteria you sent — and nothing about accuracy.
- **`ScoreAnswer` exposes two parallel slices.** They are always the same length,
  but a level missing from a response is filled in rather than reported.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security reports: see
[SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE).
