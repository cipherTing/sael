# Adversarial boundary probes — raw evidence

These probes try to **falsify the claims in `sdk/doc.go`** against the live
endpoint. They are not golden fixtures and are not written to disk by the
script; the output below is verbatim.

Reproduce with:

```bash
set -a && . ./.secrets/test.env && set +a
./sdk/testdata/golden/probe_boundary.sh
```

Environment: `TYPESAFE_BASE_URL=https://openrouter.ai/api/v1`,
`TYPESAFE_DEFAULT_MODEL=typesafe/jev-1.13`, run 2026-09-21.
Every request was a real HTTPS call. The API key is read from
`$TYPESAFE_API_KEY` and is never printed or saved.

## Raw output

```text
== doc.go:48/49/50 -- model, state, questions are required ==
no model field                           HTTP 400  error-envelope top_keys=['error', 'user_id'] message=JSON-array code=400
                                           msg='[\n  {\n    "expected": "string",\n    "code": "invalid_type",\n    "path": [\n      "model"\n    ],\n    "message": "Invalid input: expected string,
state missing                            HTTP 400  error-envelope top_keys=['error', 'user_id'] message=JSON-array code=400
                                           msg='[\n  {\n    "code": "invalid_union",\n    "errors": [\n      [\n        {\n          "expected": "string",\n          "code": "invalid_type",\n      
state = null                             HTTP 400  error-envelope top_keys=['error', 'user_id'] message=JSON-array code=400
                                           msg='[\n  {\n    "code": "invalid_union",\n    "errors": [\n      [\n        {\n          "expected": "string",\n          "code": "invalid_type",\n      
state = number 42                        HTTP 400  error-envelope top_keys=['error', 'user_id'] message=JSON-array code=400
                                           msg='[\n  {\n    "code": "invalid_union",\n    "errors": [\n      [\n        {\n          "expected": "string",\n          "code": "invalid_type",\n      
state = nested object                    HTTP 200  200 answers=['q']
questions missing entirely               HTTP 400  error-envelope top_keys=['error', 'user_id'] message=JSON-array code=400
                                           msg='[\n  {\n    "expected": "record",\n    "code": "invalid_type",\n    "path": [\n      "questions"\n    ],\n    "message": "Invalid input: expected rec
questions = {}                           HTTP 400  error-envelope top_keys=['error', 'user_id'] message=JSON-array code=400
                                           msg='[\n  {\n    "code": "custom",\n    "path": [\n      "questions"\n    ],\n    "message": "At least one question is required"\n  }\n]'

== doc.go:57 -- score criteria '>= 2 entries' ==
score, 0 criteria entries                HTTP 400  error-envelope top_keys=['error', 'user_id'] message=JSON-array code=400
                                           msg='[\n  {\n    "origin": "array",\n    "code": "too_small",\n    "minimum": 1,\n    "inclusive": true,\n    "path": [\n      "questions",\n      "q",\n 
score, 1 criteria entry                  HTTP 200  200 answers=['q']
score, 2 criteria entries                HTTP 200  200 answers=['q']
score, criteria missing                  HTTP 400  error-envelope top_keys=['error', 'user_id'] message=JSON-array code=400
                                           msg='[\n  {\n    "expected": "array",\n    "code": "invalid_type",\n    "path": [\n      "questions",\n      "q",\n      "criteria"\n    ],\n    "message"

== doc.go:55 -- noul criteria shape ==
noul, criteria missing                   HTTP 200  200 answers=['q']
noul, criteria = null                    HTTP 400  error-envelope top_keys=['error', 'user_id'] message=JSON-array code=400
                                           msg='[\n  {\n    "expected": "object",\n    "code": "invalid_type",\n    "path": [\n      "questions",\n      "q",\n      "criteria"\n    ],\n    "message
noul, criteria = object                  HTTP 200  200 answers=['q']

== doc.go:56 -- choice criteria shape ==
choice, criteria = {}                    HTTP 400  error-envelope top_keys=['error'] message=PLAIN-STRING code=400
                                           msg='HTTP 400: {"detail":"Choice question must have at least one choice: q"}'
choice, criteria missing                 HTTP 400  error-envelope top_keys=['error', 'user_id'] message=JSON-array code=400
                                           msg='[\n  {\n    "expected": "record",\n    "code": "invalid_type",\n    "path": [\n      "questions",\n      "q",\n      "criteria"\n    ],\n    "message
choice, criteria = array                 HTTP 400  error-envelope top_keys=['error', 'user_id'] message=JSON-array code=400
                                           msg='[\n  {\n    "expected": "record",\n    "code": "invalid_type",\n    "path": [\n      "questions",\n      "q",\n      "criteria"\n    ],\n    "message

== doc.go:53 -- strictness of the request decoder ==
unknown top-level field                  HTTP 200  200 answers=['q']
unknown field in a question              HTTP 200  200 answers=['q']

== non-JSON request body ==
truncated JSON body                      HTTP 400  error-envelope top_keys=['error', 'user_id'] message=JSON-array code=400
                                           msg='[\n  {\n    "expected": "record",\n    "code": "invalid_type",\n    "path": [\n      "questions"\n    ],\n    "message": "Invalid input: expected rec
```

## What this proves, and what it does not

### Confirmed claims (the doc is right here)

- `model` is required and must be a string — omitting it gives 400 (doc.go:48).
- `state` is required, and must be a string, object or array — omitted, `null`
  and a bare number all give 400; a nested object gives 200 (doc.go:49).
- `questions` is required and must be non-empty — omitted and `{}` both give 400
  (doc.go:50, and the `question_empty_questions.json` fixture).
- `noul` and `choice` `criteria` must be objects when present; arrays are
  rejected (doc.go:55-56).
- `score` `criteria` must be an array when present (doc.go:57).
- The error envelope for validation failures is `{"error":{"message":…,"code":…}}`
  with `code` numerically equal to the HTTP status (doc.go:92-94).

### Discrepancies (the doc is wrong or incomplete here)

**1. `score` requires 1 criterion, not 2 — `doc.go:57` is wrong.**

`doc.go:57` documents `"criteria":[<entry>,<entry>,...]  // >= 2 entries`.
Live: **1 entry → HTTP 200**; 0 entries → 400 with a zod `too_small` issue
whose `minimum` field is `1`. So the server's real minimum is 1, and a client
that rejects a 1-entry rubric (as `task-2` requirement #5 instructs) refuses a
request the server would happily answer. This is a client-side over-restriction
relative to the live API, and it is written into the frozen spec — so it is a
**spec bug, not an implementer's bug**. Whoever owns `doc.go:57` and
`questions.go`'s `ValidateQuestions` should decide which to follow; the live
behaviour is the only falsifiable evidence.

**2. "message is a JSON-encoded array of issues" is not always true — `doc.go:95-97`.**

A `choice` question with `criteria: {}` returns HTTP 400 whose
`error.message` is the plain string

```
HTTP 400: {"detail":"Choice question must have at least one choice: q"}
```

That is not JSON — it wraps an embedded FastAPI-style `{"detail": …}` body and
has a `HTTP 400: ` prefix. `doc.go:95-97` promises the message is always a
JSON-encoded array of issues. Any code that assumes it can `json.Unmarshal` the
message (e.g. to extract structured issues) will fail on this path. This is also
the one validation error observed **without** the `user_id` key.

Note the diagnostic is still preserved verbatim in `error.message`, so a client
that just surfaces the raw string is fine — it just must not parse it
unconditionally.

**3. `noul` `criteria` is optional — `doc.go:55` implies otherwise.**

`doc.go:55` prints `criteria` inside the `noul` shape with no "optional" marker,
next to fields the text explicitly calls required. Live: omitting `criteria`
entirely → **HTTP 200** (this is exactly the `noul_string.json` fixture). So
`NoulQuestion.Criteria` being a pointer is correct, and nothing may require it.
When present it must be an object; `"criteria": null` → 400.

**4. Extra top-level `user_id` in some error bodies — undocumented.**

Zod-validation 400s carry `{"error": {…}, "user_id": "user_…"}`. `doc.go:92`
documents only `{"error": {…}}`. `user_id` is **absent** on
`error_model_not_found` (400), `error_no_auth` (401) and the choice-empty-
criteria 400. A strict decoder using `DisallowUnknownFields` would fail on the
common validation path.

**5. `legend` echoes criteria verbatim — `doc.go:73` is misleading.**

`doc.go:73` shows `"legend":{"0":"none","1":"mild","2":"serious","3":"severe"}`,
i.e. short labels. Live responses echo the criteria entries **including their
`"0: "` prefixes**:

```json
"legend": {"0": "0: No problem at all.", "1": "1: Minor cosmetic or timing issue, no real impact.", …}
```

See `score_rubric.json` and `chinese_state.json`. Do not strip prefixes, do not
assume short labels, do not assume the legend text equals any label you sent.

**6. `error_no_auth` wording is cookie-flavoured.**

`error_no_auth.json` returns `"message":"No cookie auth credentials found"` for
a request that carried no `Authorization` header at all. Not a contradiction of
`doc.go` (which does not pin the message), but worth knowing before matching on
message text.

### Not probed (explicitly out of scope here)

- The official host `https://api.typesafe.ai/v1` — all evidence here is through
  the OpenRouter gateway. Claims about `provider` / `id` / `usage.cost` being
  gateway-only are inferred from `doc.go:85-88`, **not** independently verified.
- `Retry-After`, 429 and 5xx behaviour: not induced against the live endpoint.
  Those belong in `transport_test.go` against `httptest`.
- Whether a 1-entry `score` rubric returns a sane `legend`/`probabilities`: the
  probe only checked the status code, not the answer body.
