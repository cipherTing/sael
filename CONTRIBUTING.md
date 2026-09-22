# Contributing

Thanks for taking the time to look. This is a small project, so the process is
deliberately light.

## Getting set up

Go 1.23 or newer.

```sh
git clone https://github.com/cipherTing/sael
cd sael
make help      # lists every target
make check     # everything CI runs except the live tests
```

The repository holds two modules, `sdk/` and `cli/`, joined by a `replace`
directive in `cli/go.mod`. Every target runs once per module, so a failure names
which one broke. There is deliberately no committed `go.work`: workspace mode is
incompatible with `GOFLAGS=-mod=mod`, a setting from before Go 1.16 that is still
common, and its error message does not hint at the cause.

`make check` downloads its linters and its release tool into shared caches — the
module cache, and `~/.cache/sael/tools` — rather than installing anything into the
clone. The versions are pinned in the Makefile, which is also what CI calls, so the
two cannot disagree about what "clean" means.

## Before opening a pull request

Run:

```sh
make check
```

That covers formatting, a tidy `go.mod` in both modules, `go vet`, staticcheck,
golangci-lint, a check of the workflow files themselves, and the offline test suite
under the race detector. CI runs exactly the same set, plus a build and test on
Linux, macOS and Windows.

The live tests are separate, because they need a key:

```sh
TYPESAFE_API_KEY=... TYPESAFE_BASE_URL=... make test-integration
```

`TYPESAFE_BASE_URL` can point at any host that speaks the API. Do not commit a
key, and do not put one in a test; the suite is written to run offline and the
live tests skip themselves when the environment is not set up.

## What tends to get merged

- **A test that fails before the change and passes after it.** For a bug fix, the
  failing test is the part that matters most; the fix is usually the easy half.
  Prove it: break the code on purpose, watch the test fail, then restore it.
- **A test that cannot pass for the wrong reason.** The whole suite runs with
  `TYPESAFE_API_KEY`, `TYPESAFE_BASE_URL` and `TYPESAFE_DEFAULT_MODEL` poisoned,
  `SAEL_HOME` pointing nowhere, `NO_COLOR=1` and `TERM=dumb`. A test whose outcome
  depends on the developer's environment is a test that will be believed when it
  is wrong, so assert on what you set up rather than on what you inherited.
- **A comment explaining why, when the code cannot.** This package has a lot of
  them, because most of it encodes decisions made against a live API that a reader
  cannot reproduce: which shape the host rejects, which field is only present
  through a gateway, which behaviour is deliberate rather than accidental. When
  you remove one of those comments, be sure the reason is still obvious.
- **Small, focused commits.** One idea each.

## What to avoid

- **New dependencies.** The SDK has two, both deliberate: `backoff/v5` for the
  backoff schedule and `testify` in tests. The CLI adds `cobra` for the command
  tree and `x/term` for terminal detection. Anything else needs a reason that
  survives being written down, particularly in the library rather than its tests.
- **Baking in a host.** `BaseURL` is configuration on purpose. A default that
  points at one vendor, a special case for one gateway's behaviour, or an
  assumption that a field the official schema does not list will always be
  present — all of those break callers who point the client elsewhere.
- **Silently ignoring an error.** Wrap it with `%w` and let the caller decide.

## Reporting a bug

Include the version (`sdk.Version()`), the model id from the response, and
what you expected. If the problem is a decoding failure, paste the raw response
body — but **redact your key**, and remember that `state` often contains the
content being classified.

For anything security-related, see [SECURITY.md](SECURITY.md) instead of opening
an issue.
