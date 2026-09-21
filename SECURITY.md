# Security Policy

## Reporting a vulnerability

Please **do not** open a public issue for a security problem. Use GitHub's
[private vulnerability reporting](https://github.com/cipherTing/sael/security/advisories/new)
instead, so there is a chance to fix it before it is public.

Please include what you did, what happened, and what you expected, plus the
version from `systemone.Version()` and the Go version. A minimal reproducer is
worth more than a long description.

You should get a reply within a few days. This is a small project maintained in
spare time, so there is no formal SLA, but a credible report will not be ignored.

## Scope

Things that are in scope, roughly in the order they would matter:

- **Anything that leaks an API key.** Into a log line, an error message, a
  `%v` of a request, a redirected request, a panic.
- **Anything that could bypass the client's own guardrails.** The client refuses
  redirects on purpose, because Go's default policy rewrites a `301`/`302`/`303`
  POST into a GET with no body and replays the `Authorization` header at whatever
  location the server named. A way around that refusal is a finding.
- **A caller being able to replace or append to `Authorization`.** Caller-supplied
  headers must not be able to add a second one, because a proxy that reads the
  last value would authenticate with the caller's token. There are regression
  tests for this; a way past them is a finding.
- **Denial of service against the caller.** Unbounded allocation, a hang, a panic
  reachable from a hostile or merely broken response.
- **A path traversal in the configuration directory.** `LoadConfig` reads a fixed
  filename from a caller-supplied directory; if you can make it read somewhere
  else, that is a finding.

## Out of scope

- **What the model decides.** This client is a transport: it does not judge
  whether an answer is correct or appropriate, and a classification you disagree
  with is not a vulnerability in this package.
- **The security of the API service itself.** Report that to the service
  operator.
- **The fact that the API key is stored unencrypted in `~/.Sael/auth.json`.**
  That is the same bargain `ssh` and the other agent CLIs make: the protection is
  the file's mode and the directory it sits in, not a password. On Unix the mode
  is `0600` and the directory `0700`. On Windows there are no such bits — `Chmod`
  only toggles the read-only attribute there — so the file relies on the ACL of
  the user's profile directory and on nothing this package does. That is a known
  gap, not a secret one. What *is* a finding either way is the file being created
  or replaced in a way that briefly exposes it: `SaveAuth` writes through a
  temporary file created with the restrictive mode for exactly that reason.
- **Dependency vulnerabilities with no reachable path** in this package. The
  runtime dependency graph is one module, `github.com/cenkalti/backoff/v5`, which
  itself has none.

## What this package assumes

Worth stating explicitly, because it shapes what counts as a bug:

- The caller's process is trusted. A caller can read the key from memory, and no
  amount of care in this package prevents that.
- The host at `BaseURL` is trusted to the extent that it receives the API key.
  Pointing `BaseURL` at a hostile host hands it the credential; that is
  configuration, not a vulnerability.
- `state` typically contains the content being classified, which may be personal
  data. This package logs request and response bodies at debug level only, and
  never logs credential headers. Do not enable debug logging on untrusted
  content.
