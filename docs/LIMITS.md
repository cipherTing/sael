# Known limits

sael is early software. Everything below is known, and none of it is a bug waiting
to be found.

## Not built, and refused by name

The release matrix is **darwin/arm64, linux/amd64 and windows/amd64**. Intel Macs
and ARM Linux are out of scope for now. The installers refuse an unbuilt platform
by name and list what is available, rather than installing a binary that will not
run.

## Accuracy is unmeasured

- **The question set has never been run against real traffic.** Its false-positive
  rate on the requests a relay actually receives is the number that matters, and
  nobody has it yet. What is known is that narrow decomposition of a category
  raises false positives sharply — a measured 37% on hard-benign input in one
  experiment, against 1.5% for a single direct question — which is why every
  question carries its benign readings explicitly.
- **No language has been validated specifically.** The model is documented as
  handling CJK "not equally well", and a threshold calibrated on one language
  should not be assumed to hold in another. Per-language calibration is untested.
- **The package makes no judgement about whether an answer is correct.** The tests
  assert structural invariants — probabilities in range, distributions summing to
  one, a `Legend` matching the criteria that were sent — and nothing about
  accuracy.
- **Jev cannot reliably separate mentioning from using.** A novelist and someone
  seeking operational detail can produce the same words. The graded questions
  describe a request's shape; they do not resolve that ambiguity.

## Tested only through a gateway, or only offline

- **The live tests have only ever run through one gateway.** The behaviour the
  client is written against was observed going through OpenRouter, not against the
  official host directly. Where the two disagreed, this package follows the
  documented reference and records the divergence where it occurs, in
  [sdk/doc.go](../sdk/doc.go) and the fixtures under
  [sdk/testdata/golden](../sdk/testdata/golden).
- **Retry, timeout, redirect and rate-limit handling are covered only by
  `httptest`.** No real network failure mode has been exercised.
- **`install.ps1`'s Windows path is exercised only in CI.** The download, the
  archive extraction, the user `PATH` edit and the console handling cannot be run
  on a Unix machine, so they are verified on a Windows runner and nowhere else.
- **Both installers' release-resolution fallbacks are unexercised**, because they
  only run when a published release is unreachable.

## Platform caveats

- **The configuration file mode is a Unix guarantee only.** On Windows `Chmod`
  cannot express `0600`, so `auth.json` relies on the ACL of the profile
  directory. Closing this would mean an ACL dependency, which the package does not
  take.

## API shapes that may surprise

- **`ScoreAnswer` exposes two parallel slices.** They are always the same length,
  but a level missing from a response is filled in rather than reported.
- **A `Score` is an expectation, not an index.** It may fall between levels, so it
  is never rounded into a position in `Legend`. Read the level name from the answer
  rather than computing it from the number.
