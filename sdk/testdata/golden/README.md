# Golden fixtures — `sdk`

Real, unedited snapshots of the TypeSafe "System One" (Jev) endpoint, captured by
`capture.sh`. They are the ground truth for the offline conformance tests in
`sdk/conformance_test.go`.

## Provenance

| | |
|---|---|
| Captured on | **2026-09-21** (UTC), single capture run |
| Captured by | `verifier` (independent verification role), via `./capture.sh` |
| Transport | `POST https://openrouter.ai/api/v1/systemone`, `Authorization: Bearer …` |
| Model requested | `typesafe/jev-1.13` |
| Model resolved by the server | **`typesafe/jev-1.13-20260917`** (identical in all 8 successful responses) |
| Capture method | live HTTPS calls only — **nothing here is hand-written** |
| Credentials | read from `$TYPESAFE_API_KEY` at capture time; never written to any file |

Reproduce with:

```bash
set -a && . ./.secrets/test.env && set +a
./sdk/testdata/golden/capture.sh
```

`capture.sh` is vendor neutral: it reads `TYPESAFE_BASE_URL` and
`TYPESAFE_DEFAULT_MODEL` (defaulting to the OpenRouter gateway and
`typesafe/jev-1.13`) and `TYPESAFE_API_KEY`. Re-running it **will** change the
`id`, `usage` and the exact probability values — those are not deterministic.

## File format

Every fixture is a JSON object:

```json
{
  "note": "why this case exists",
  "request": { "model": "...", "state": ..., "questions": {...} },
  "request_authorized": true,
  "status": 200,
  "response": { ... }
}
```

- `request` is exactly the JSON body that was sent (no headers).
- `response` is the parsed JSON body that came back. If a body had not been
  valid JSON it would be stored as a string; no fixture needed that.
- `request_authorized` is `false` **only** for `error_no_auth.json`, which was
  deliberately sent without an `Authorization` header. No credential material,
  and no `Authorization`/`Bearer` header value, appears anywhere in this
  directory — see the self-check at the bottom.
- `note` and `request_authorized` are capture metadata. Consumers that decode a
  fixture should read `request`, `response` and `status` and ignore the rest.

## The fixtures

| File | Status | Purpose |
|---|---|---|
| `noul_string.json` | 200 | `noul` primitive, `instructions` a plain string, **no `criteria` key at all** |
| `noul_criteria.json` | 200 | `noul` with explicit `criteria: {"true": …, "false": …}` |
| `choice_null_description.json` | 200 | `choice` primitive; one option (`other`) has a `null` description |
| `score_rubric.json` | 200 | `score` primitive with a 4-level rubric; returned `score` is **1.25, a fraction** |
| `mixed_primitives_object_state.json` | 200 | all three primitives in one call; `state` is a **JSON object** |
| `state_string.json` | 200 | `state` is a bare JSON string |
| `state_array.json` | 200 | `state` is a **JSON array of strings** |
| `chinese_state.json` | 200 | long Chinese `state`, English questions — baseline for Chinese-language behaviour |
| `error_model_not_found.json` | 400 | non-existent model id |
| `error_bad_question_type.json` | 400 | `{"type":"bogus"}` |
| `error_empty_questions.json` | 400 | `"questions": {}` |
| `error_no_auth.json` | 401 | sent with **no** `Authorization` header |

`chinese_state.json` is the only fixture that exercises non-ASCII input; note
that its `state` is stored as raw UTF-8 (`ensure_ascii=False`), so the file
itself is UTF-8, not `\uXXXX` escapes.

## Gateway-added fields — important

The fixtures were captured **through the OpenRouter gateway**, not against the
official host. Three fields in the success body are gateway additions rather
than part of the official API shape:

- `provider` (`"TypeSafe"`)
- `id` (e.g. `"gen-dec-1789964236-rZVpEMgm93YsIgPL1UnP"`)
- `usage.cost` (e.g. `1.2054e-05`)

They are **kept verbatim** in these fixtures on purpose: a client must decode
them when present and leave them zero/empty when absent, so that the same code
works against the official host directly. Conformance tests should therefore
assert these fields **only** for fixtures that contain them, and must not treat
their absence as an error. `usage.cost` is the reason `Usage.Cost` has to be a
pointer.

`model`, `answers` and `usage.input_tokens` / `usage.output_tokens` are part of
the official shape and are always present here.

## These are point-in-time snapshots, and the model drifts

**The fixtures are real responses from one moment in time.** The server resolved
`typesafe/jev-1.13` to the pinned build `typesafe/jev-1.13-20260917` on
2026-09-21. `jev-latest`-style aliases and pinned builds both move: a future
capture may return a different `model` string and different probabilities,
`confidence` values, `score` expectations and token counts.

Consequences for test authors:

- Assert **structure and invariants** (key sets, answer `type`, probability
  ranges, `legend` length) against these fixtures — not the literal
  probabilities, and not `provider`/`id` as fixed strings beyond keeping them in
  sync with the fixture.
- Nothing here should be used to assert that a *model* is correct or stable.
  Only the wire shape is being frozen.
- If a fixture ever stops matching the live API, re-capture rather than editing
  the JSON by hand.

## Contract discrepancies found while capturing

Full raw evidence, with commands and unedited output, is in
[`PROBES.md`](./PROBES.md). Summary of where the live endpoint and
`sdk/doc.go` disagree:

1. **`doc.go:57` says a `score` question needs `>= 2` criteria entries. The
   live API accepts exactly 1** (HTTP 200) and rejects 0 (`400`, zod
   `too_small`, `minimum: 1`). The real minimum is **1**.
2. **`doc.go:95-97` says validation failures return HTTP 400 with `message` set
   to a JSON-encoded array of issues.** A `choice` question with an empty
   `criteria` object instead returns `message` = the plain string
   `HTTP 400: {"detail":"Choice question must have at least one choice: q"}`,
   which is not JSON at all.
3. **`noul` `criteria` is optional.** `doc.go:55` lists it inside the `noul`
   shape without marking it optional; the live API returns 200 when it is
   omitted. When present it must be an object (`null` → 400).
4. Some validation errors carry an extra top-level `user_id` key next to
   `error`; `doc.go:92` documents only `{"error": {…}}`. `user_id` appears on
   zod validation failures but **not** on `error_model_not_found` (400) or
   `error_no_auth` (401), and not on the `choice`-empty-criteria 400 above.
5. `doc.go:73` shows short `legend` labels (`"none"`, `"mild"`). The live
   server echoes the criteria entries **verbatim**, including their `"0: "`
   prefixes — see `score_rubric.json`. Do not assume the server shortens labels.
6. `error_no_auth.json`'s message is `"No cookie auth credentials found"`,
   i.e. cookie/browser-flavoured wording even for a Bearer-token API.

## Self-check

Both checks below pass, and their literal search patterns are deliberately not
reproduced in this file, because doing so would itself make the second check
fail.

```console
$ for f in sdk/testdata/golden/*.json; do python3 -m json.tool "$f" >/dev/null && echo "OK $f"; done
OK   ...                                  # all 12 files

$ grep -r '<the OpenRouter key prefix, "sk-" + "or-v1">' sdk/testdata/
                                          # no output, exit status 1

$ grep -rE 'Bearer [A-Za-z0-9_.-]' sdk/testdata/
                                          # no output, exit status 1
```
