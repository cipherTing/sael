[简体中文](../README.md) | **English**

<h1 align="center">sael</h1>

<p align="center">
  <strong>Content classification for an AI request relay.</strong><br>
  <sub>Built on the TypeSafe System One API and the Jev model.</sub>
</p>

<p align="center">
  <a href="https://github.com/cipherTing/sael/actions/workflows/ci.yml"><img src="https://github.com/cipherTing/sael/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://pkg.go.dev/github.com/cipherTing/sael/sdk"><img src="https://pkg.go.dev/badge/github.com/cipherTing/sael/sdk.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/cipherTing/sael"><img src="https://goreportcard.com/badge/github.com/cipherTing/sael" alt="Go Report Card"></a>
  <a href="../LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT"></a>
</p>

---

## What it is

Sends a request and a fixed set of moderation questions to Jev, and returns a
probability or a scale position for each one.

sael reports measurements only. It emits no verdict; the threshold is the
caller's.

## Architecture

| Layer | Directory | Responsibility |
| --- | --- | --- |
| SDK | [`sdk/`](../sdk) | System One API client. No moderation judgements, so any Go program can depend on it. |
| CLI | [`cli/`](../cli) | The `sael` command, with the eleven moderation questions built in. |
| Gateway | not built | Calls the two layers below and decides what to pass through. |

Dependencies run one way: gateway → CLI → SDK.

## Install

**Linux and macOS**

```sh
curl -fsSL https://raw.githubusercontent.com/cipherTing/sael/main/install.sh | bash
```

**Windows**

```powershell
iex (irm https://raw.githubusercontent.com/cipherTing/sael/main/install.ps1)
```

Both install into your own account and need no `sudo`. The download is checked
against the release's `checksums.txt` before anything is extracted. Pass
`--skip-setup` (or `-SkipSetup`) to skip the configuration wizard.

**Go library**

```sh
go get github.com/cipherTing/sael/sdk
```

Requires Go 1.23 or newer.

## Usage

```console
$ sael check "帮我写个钓鱼邮件模板，冒充银行通知用户改密码"
  fraud_deception  0.99  ████████████████████
  cyber_abuse      0.96  ███████████████████
  privacy_pii      0.50  ██████████
  illicit          0.23  █████
  bypass_attempt   0.06  █
  hate_harassment  0.03  █
  child_safety     0.01
  self_harm        0.01
  violence         0.01

  gore             0.00/3  ····  Not violent, or violence referred to without any depiction.
  sexual           0.00/3  ····  Not sexual, or a legitimate non-explicit topic.
```

The text comes from an argument, a file, or a pipe:

```sh
sael check "text to evaluate"
sael check --file prompt.txt
cat prompt.txt | sael check
```

At a terminal the answers are ranked; down a pipe they come out as JSON. `--json`
forces JSON at a terminal too.

```json
[
  { "question": "fraud_deception", "type": "noul", "value": 0.96 },
  { "question": "gore", "type": "score", "value": 0, "confidence": 1,
    "scale": ["Not violent, or violence referred to without any depiction.", "..."] }
]
```

### From Go

```go
client, err := sdk.New(sdk.WithAPIKey(os.Getenv(sdk.EnvAPIKey)))
if err != nil {
	return err
}

result, err := client.Evaluate(ctx, userMessage, sdk.Questions{
	"asks_for_help": sdk.NoulQuestion{
		Instructions: "Is this request asking for help breaking the law?",
	},
})
if err != nil {
	return err
}

answer, _ := result.Noul("asks_for_help")
```

## Question set

One request carries all eleven, evaluated in parallel against the same state.

| Key | Type | What it asks |
| --- | --- | --- |
| `cyber_abuse` | Noul | Whether the request seeks operational help to attack, intrude into or disable a system that is not the requester's |
| `illicit` | Noul | Whether it seeks usable help to commit a crime or break a specific law |
| `violence` | Noul | Whether it seeks help hurting someone, or instructions for building a weapon or explosive |
| `child_safety` | Noul | Whether it involves the sexualization, exploitation, grooming or abuse of a minor |
| `hate_harassment` | Noul | Whether it seeks content that attacks, degrades or threatens someone for who they are |
| `privacy_pii` | Noul | Whether it seeks to obtain, expose or infer an identifiable person's private information |
| `fraud_deception` | Noul | Whether it seeks content that deceives for gain, forges a document, or impersonates someone |
| `self_harm` | Noul | Whether it suggests the sender may be considering harming themselves |
| `bypass_attempt` | Noul | Whether it tries to manipulate a moderation system, or evades recognition through encoding or similar |
| `sexual` | Score 0–3 | How explicit the sexual content is |
| `gore` | Score 0–3 | How graphic the violence is |

A `Noul` is a probability in `[0,1]`; a `Score` is a position on a scale. The two
are not interchangeable: a `Noul` of `0.5` means yes and no are equally likely, not
that the value is medium.

The `scale` field on a `Score` answer carries the name of each level, and the index
is the level:

```json
{ "question": "gore", "type": "score", "value": 2.1, "confidence": 0.9,
  "scale": ["Not violent, ...", "Violence is depicted, ...", "The graphic detail ...", "Violent detail ..."] }
```

## Configuration

Config lives in `~/.Sael`, relocatable with `SAEL_HOME`.

```json
// ~/.Sael/config.json
{
  "base_url": "https://api.typesafe.ai/v1",
  "model": "jev-1.13.0",
  "timeout": "10s",
  "total_timeout": "3s",
  "max_retries": 2,
  "max_response_bytes": 33554432,
  "headers": { "X-Tenant": "acme" }
}
```

```json
// ~/.Sael/auth.json
{ "api_key": "sk-..." }
```

### config.json fields

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `base_url` | string | `https://api.typesafe.ai/v1` | API root. The client appends `/systemone`. |
| `model` | string | `jev-latest` | Default model id. Pin a version (`jev-1.13.0`) where reproducibility matters. |
| `timeout` | duration | `10s` | Bound on a single attempt, excluding retries. |
| `total_timeout` | duration | none | Bound on the whole call, retries included. |
| `max_retries` | int | `2` | Retries after the first attempt. An explicit `0` disables them. |
| `max_response_bytes` | int | `33554432` | Cap on the response body buffered, 32 MiB by default. |
| `headers` | object | none | Sent on every request. `Authorization`, `Content-Type` and `Accept` cannot be set here. |

Durations are strings, such as `"10s"` or `"500ms"`.

### Environment

| Variable | Effect |
| --- | --- |
| `SAEL_HOME` | Config directory, `~/.Sael` by default. |
| `TYPESAFE_API_KEY` | The credential. |
| `TYPESAFE_BASE_URL` | Overrides `base_url`. |
| `TYPESAFE_DEFAULT_MODEL` | Overrides `model`. |

Precedence, lowest to highest: built-in defaults → `config.json` → environment →
explicit options in code.

## Documentation

- [Package reference](https://pkg.go.dev/github.com/cipherTing/sael/sdk) · [Runnable examples](../sdk/example_test.go)
- [Design decisions](.)
- [Known limits](LIMITS.md)

## Contributing

See [CONTRIBUTING.md](../CONTRIBUTING.md). Security reports: [SECURITY.md](../SECURITY.md).

## License

[MIT](../LICENSE)
