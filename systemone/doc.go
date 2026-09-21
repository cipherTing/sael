// Package systemone is a Go client for the TypeSafe "System One" decision API
// (the Jev model family).
//
// It is a hand-written, dependency-light port of the official TypeScript SDK
// (github.com/typesafe-ai/typesafe-sdk-js), scoped to the single endpoint this
// project needs: POST {baseURL}/systemone.
//
// # Vendor neutrality
//
// Nothing about a particular host is baked in. The client speaks the API shape
// and nothing more, so BaseURL can point at the official host, at a gateway that
// re-exposes the same endpoint, at a proxy, or at a local test server, with no
// other change.
//
// Gateways may add fields to a success response that the official host does not
// send — "provider", "id", and "usage.cost" are the ones observed so far. Those
// are decoded when present and left zero when absent, so one client works
// against both without configuration.
//
// # What this model is
//
// Jev does not generate text. You send a state (the material to judge) and a
// set of typed questions; it returns typed answers with probabilities. Three
// question primitives exist and may be mixed in one call:
//
//	Noul  — a yes/no judgement          returns a probability in [0,1]
//	Choice— pick one of named options   returns choice + probabilities + confidence
//	Score — place the state on a rubric returns score + legend + probabilities + confidence
//
// Every question is evaluated independently and in parallel against the same
// state, so adding questions barely changes latency. Ask several narrow
// questions rather than one broad one, and combine the answers in your own code.
//
// # Wire contract
//
// The schema is the official System One API's, as documented at
// https://docs.typesafe.ai/api. Every implementation file in this package is
// written against it; do not guess at it. The behaviour was additionally
// verified against a live endpoint on 2026-09-21 reached through a gateway, and
// the two disagreed in the places called out below.
//
// Request:
//
//	POST {BaseURL}/systemone
//	Authorization: Bearer <api key>
//	Content-Type: application/json
//
//	{
//	  "model": "jev-latest",                // required, non-empty string
//	  "state": <string | object | array>,   // required; passed through untouched
//	  "questions": { "<name>": <question> } // required, non-empty
//	}
//
// Question variants (discriminated by "type"). An <entry> is a string, a JSON
// object or a JSON array; null is additionally allowed as a choice description,
// but not as either side of a noul criteria:
//
//	{"type":"noul",   "instructions":<entry>, "criteria":{"true":<entry>,"false":<entry>}}
//	{"type":"choice", "instructions":<entry>, "criteria":{"<label>":<entry|null>}}
//	{"type":"score",  "instructions":<entry>, "criteria":[<entry>,<entry>,...]}
//
// with these limits:
//
//	noul   criteria optional; when present, BOTH true and false are required
//	choice criteria required, 1..255 options
//	score  criteria required, 2..10 levels
//
// Three traps the live endpoint exposed, all covered by tests in this package:
//
//   - A noul's "criteria" is all or nothing. Omitting the key entirely is valid;
//     sending the key with a null side, or with only one side, is a hard
//     validation failure, and so is sending the whole criteria as null. In Go the
//     field is a pointer with omitempty, because a nil pointer without it
//     marshals to null and fails every such request.
//   - A Score with a single level was accepted by the host used for verification,
//     while the official reference says a Score needs at least two levels and
//     accepts up to ten. The host was not taken as the contract:
//     ValidateQuestions enforces the documented minimum. The official host's own
//     behaviour was never exercised, so treat that gap as gateway variance, not
//     as something settled about the API.
//   - The noul "criteria" sides are not interchangeable with a choice's option
//     descriptions: a choice option may be null, a noul side may not.
//
// Success response (HTTP 200), official shape:
//
//	{
//	  "model":   "jev-1.13.0",
//	  "answers": {
//	    "<name>": {"type":"noul","noul":0.73}
//	           |  {"type":"choice","choice":"billing",
//	               "probabilities":{"other":0,"tech":0,"billing":1},"confidence":1}
//	           |  {"type":"score","score":0.25,
//	               "legend":{"0":"none","1":"mild","2":"serious","3":"severe"},
//	               "probabilities":{"0":0.76,"1":0.24,"2":0,"3":0},"confidence":0.75}
//	  },
//	  "usage":   {"input_tokens":405,"output_tokens":68}
//	}
//
// Three details matter and are easy to get wrong:
//
//   - "legend" and "probabilities" are objects keyed by the decimal score as a
//     STRING ("0".."n"). They are decoded here into slices indexed by score.
//     The legend echoes the criteria descriptions back verbatim; do not assume
//     they are short labels, and do not strip anything from them.
//   - "score" is an expectation and may be fractional (0.25 above) even though
//     the legend is discrete. Do not round it into an index.
//   - "legend" reflects what you sent, so the number of levels is yours.
//
// Hosts may return fields the official schema does not list. Observed on one:
// "provider", "id" (a request identifier) and "usage.cost". Result.Provider,
// Result.ID and Usage.Cost decode them and stay empty or nil when a host omits
// them, so no configuration is needed either way. Nothing in this package
// depends on them.
//
// Error response (non-2xx):
//
//	{"error":{"message":"Model nope does not exist","code":400}}
//
// The official reference documents 401, 422 (validation), 429 and 529
// (overloaded); the host used for verification returned 400 where the reference
// says 422. Status codes are therefore reported rather than interpreted, with
// the four codes above only steering retry behaviour.
//
// The "code" field mirrors the status but is not guaranteed to be numeric, so it
// is decoded as raw JSON. Some responses carry a top-level "user_id" alongside
// "error"; decoding does not reject unknown fields, so this is tolerated.
//
// A validation failure's "message" is USUALLY a JSON-encoded array of issues,
// but not always: at least one host returns a plain string with an embedded
// payload, such as
//
//	HTTP 400: {"detail":"Choice question must have at least one choice: q"}
//
// so it is preserved verbatim in APIError.Message and never parsed. Treat it as
// diagnostic text for a human, not as a stable machine-readable structure.
//
// # Public API
//
// The surface below is frozen; every file in this package is written against it.
//
//	New(opts ...Option) (*Client, error)
//	(*Client).Evaluate(ctx, state any, questions Questions, opts ...CallOption) (*Result, error)
//
// Configured through Option values (WithAPIKey, WithBaseURL, WithModel,
// WithHTTPClient, WithTimeout, WithTotalTimeout, WithMaxResponseBytes,
// WithRetryPolicy, WithHeader, WithLogger) and overridden per call with
// CallOption values.
//
// Answers are read through typed accessors that also report whether the name
// existed and carried the expected answer type:
//
//	(*Result).Noul(name)   (NoulAnswer, bool)
//	(*Result).Choice(name) (ChoiceAnswer, bool)
//	(*Result).Score(name)  (ScoreAnswer, bool)
//
// # Configuration on disk
//
// Settings may live in a directory, by default ~/.Sael, laid out like the other
// agent CLIs:
//
//	~/.Sael/config.json   settings that are safe to read, commit or paste
//	~/.Sael/auth.json     the credential, in its own file
//
// Two files rather than one, so the half people share is never the half that
// authenticates them. SaveConfig and SaveAuth create the directory with mode
// 0700 and write through a temporary file, so a crash cannot leave a truncated
// file behind. LoadConfig and LoadAuth read it back; both treat a missing file
// as "not configured" rather than an error, but a file that exists and does not
// parse IS an error, because silently ignoring a typo in a hand-edited file is
// how people lose an afternoon.
//
// # File modes are a Unix guarantee only
//
// On Unix both files are created with mode 0600 and the directory with 0700, so
// the credential is readable only by its owner. Windows has no such bits: Chmod
// there only toggles the read-only attribute, and FileMode.Perm reports a
// synthesised 0666 or 0777 no matter what was asked for. On Windows the
// credential is therefore protected by the ACL on the user's profile directory
// and by nothing this package does. That is a real gap, stated rather than
// papered over; closing it would mean taking an ACL dependency, which this
// package does not.
//
//	// config.json
//	{
//	  "base_url": "https://api.typesafe.ai/v1",
//	  "model": "jev-latest",
//	  "timeout": "10s",
//	  "total_timeout": "3s",
//	  "max_retries": 2,
//	  "headers": {"X-Tenant": "acme"}
//	}
//
//	// auth.json
//	{"api_key": "..."}
//
// Durations are strings such as "3s" or "500ms".
//
// New performs no file I/O. Use NewFromConfigDir to have the directory loaded:
// an empty directory means DefaultConfigDir, so a command-line tool starts with
// systemone.NewFromConfigDir("") and needs no path handling of its own. Set
// SAEL_HOME to relocate the directory. A constructor that silently read a home
// directory would make every caller's behaviour depend on the machine it runs
// on, and would make a test suite depend on whoever's laptop is running it.
//
// # Defaults
//
//	BaseURL https://api.typesafe.ai/v1
//	Model   jev-latest
//	Timeout 10s per attempt (mirrors the official SDK)
//
// Layers resolve in order, each overriding the one before it: built-in defaults,
// then the files, then the environment (TYPESAFE_BASE_URL,
// TYPESAFE_DEFAULT_MODEL, TYPESAFE_API_KEY), then explicit Option values. Code
// therefore always beats a file on disk, which is what makes the file safe to
// treat as a baseline rather than an override. A model id namespaced for a
// gateway, such as "typesafe/jev-1.13", is just a string here.
//
// The official SDK retries 2 times after the first attempt on 408, 429 and
// 5xx, backing off from 500ms doubling to 5s with up to 25% jitter subtracted,
// honouring Retry-After up to 60s. DefaultRetryPolicy returns exactly that.
//
// Note that the official SDK's timeout is per attempt with no overall budget,
// which can stall a caller for tens of seconds. This client adds
// WithTotalTimeout for callers that need a bound; see Evaluate.
package systemone
