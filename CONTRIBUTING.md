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

`make check` installs its own linters into `./bin` at pinned versions, so it will
not touch the rest of your machine and cannot disagree with CI about what "clean"
means.

## Before opening a pull request

Run:

```sh
make check
```

That covers formatting, a tidy `go.mod`, `go vet`, staticcheck, golangci-lint and
the offline test suite under the race detector. CI runs exactly the same set, plus
a build and test on Linux, macOS and Windows.

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
- **A comment explaining why, when the code cannot.** This package has a lot of
  them, because most of it encodes decisions made against a live API that a reader
  cannot reproduce: which shape the host rejects, which field is only present
  through a gateway, which behaviour is deliberate rather than accidental. When
  you remove one of those comments, be sure the reason is still obvious.
- **Small, focused commits.** One idea each.

## What to avoid

- **New dependencies.** There are two, both deliberate: `backoff/v5` for the
  backoff schedule and `testify` in tests. A new one needs a reason that survives
  being written down, particularly in the library itself rather than its tests.
- **Baking in a host.** `BaseURL` is configuration on purpose. A default that
  points at one vendor, a special case for one gateway's behaviour, or an
  assumption that a field the official schema does not list will always be
  present — all of those break callers who point the client elsewhere.
- **Silently ignoring an error.** Wrap it with `%w` and let the caller decide.

## Reporting a bug

Include the version (`systemone.Version()`), the model id from the response, and
what you expected. If the problem is a decoding failure, paste the raw response
body — but **redact your key**, and remember that `state` often contains the
content being classified.

For anything security-related, see [SECURITY.md](SECURITY.md) instead of opening
an issue.
