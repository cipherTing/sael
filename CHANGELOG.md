# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Because this is a Go module, a version is a git tag: `git tag v0.2.0 && git push
--tags` is the whole release process.

## [Unreleased]

### Added

- `systemone` package: a client for the System One evaluation endpoint, covering
  the three question primitives (`NoulQuestion`, `ChoiceQuestion`,
  `ScoreQuestion`) and their typed answers.
- Functional options for the API key, base URL, model, HTTP client, per-attempt
  and total timeouts, response size limit, retry policy, headers and logger, plus
  per-call overrides.
- Error taxonomy: one sentinel per failure class, matchable with `errors.Is`,
  with `APIError`, `RateLimitError`, `ConnectionError`, `TimeoutError` and
  `AbortError` carrying the detail.
- Retry with exponential backoff and jitter, honouring `Retry-After` in both its
  seconds and HTTP-date forms, matching the reference SDK's defaults.
- Configuration in `~/.Sael` (`config.json` and `auth.json`), read explicitly by
  `NewFromConfigDir` and written through a temporary file with mode `0600` on
  Unix. Windows has no such bits, so there the credential relies on the ACL of
  the profile directory; that is documented rather than papered over.
- `Version`, derived from the build information the go command embeds, so a
  release tag changes what the client reports without anyone editing a constant.
- Tests: unit coverage, replay of responses captured from a live endpoint,
  hostile-input handling, and env-gated integration tests.

### Notes

- `New` performs no file I/O. Configuration is read only by
  `NewFromConfigDir`, so a caller's behaviour does not depend on the machine it
  runs on.
- Every error matches exactly one sentinel. `ErrConfig` is deliberately distinct
  from `ErrValidation`.

[Unreleased]: https://github.com/cipherTing/sael/commits/main
