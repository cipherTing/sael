package sdk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// Entry is one piece of question or criterion material: text, a JSON object, a
// JSON array, or null.
//
// It is deliberately an alias for any rather than a narrower type, because the
// TypeScript SDK's EntryType is a union and callers legitimately pass structs,
// maps and slices directly. Whatever is stored here is handed to encoding/json
// unchanged, so the usual encoding/json rules decide what is legal.
type Entry = any

// Question is a single typed question in a request. The interface is sealed by
// the unexported isQuestion method: the only implementations are NoulQuestion,
// ChoiceQuestion and ScoreQuestion.
type Question interface {
	isQuestion()

	// QuestionType is the wire discriminator: "noul", "choice" or "score".
	QuestionType() string
}

// Wire discriminators. Kept unexported: the frozen public surface exposes the
// type through the concrete Go types, not through string constants.
const (
	typeNoul   = "noul"
	typeChoice = "choice"
	typeScore  = "score"
)

// Limits the API documents on questions. ValidateQuestions enforces them before
// anything is sent.
const (
	// minScoreLevels is the smallest rubric the API accepts, and the smallest
	// that means anything: one level is an unordered yes/no, which is a Noul.
	minScoreLevels = 2

	// maxScoreLevels is the largest rubric the API accepts.
	maxScoreLevels = 10

	// maxChoiceOptions is the largest number of labels the API accepts in one
	// Choice question.
	maxChoiceOptions = 255
)

// maxScoreIndex caps the slice length derived from a Score answer's keys. A
// larger index means a malformed or hostile key, and honouring it would ask for
// a gigantic allocation; the request side is limited to maxScoreLevels, so any
// key this large is already out of contract. Keys above the cap are ignored like
// any other invalid key.
const maxScoreIndex = 4096

// ---------------------------------------------------------------------------
// Noul
// ---------------------------------------------------------------------------

// NoulCriteria describes the two outcomes of a Noul question.
//
// Both sides are required whenever the criteria object is sent at all, and
// neither may be null: an explicit null on either side is rejected by the live
// endpoint with HTTP 400. A nil field here therefore cannot be put on the wire,
// and ValidateQuestions rejects it before the request is built. To leave an
// outcome undescribed, pass a description like "" — or omit the whole criteria
// object by leaving NoulQuestion.Criteria nil.
type NoulCriteria struct {
	True  Entry `json:"true"`
	False Entry `json:"false"`
}

// NoulQuestion is a yes/no judgement. The answer is a probability in [0,1].
type NoulQuestion struct {
	// Instructions is the question itself. It is always written, as null when
	// unset: the official SDK's builder defaults it to null.
	Instructions Entry `json:"instructions"`

	// Criteria optionally describes what "true" and "false" mean.
	//
	// It is all or nothing. Nil omits the "criteria" key entirely, which is the
	// common shape and is valid — it must never become an explicit null, because
	// the host rejects "criteria":null with HTTP 400 (zod: expected "object",
	// received null); that is why this field is omitempty, matching the
	// TypeScript SDK's optional criteria. When the pointer is set, both True and
	// False must be non-nil: the host rejects a null on either side with HTTP
	// 400. See ValidateQuestions, which enforces that before sending.
	//
	// The contrast with Instructions is deliberate: the TS builder defaults
	// Instructions to null and always sends it, while criteria is a key that is
	// either fully present or absent.
	Criteria *NoulCriteria `json:"criteria,omitempty"`
}

func (NoulQuestion) isQuestion() {}

// QuestionType implements Question.
func (NoulQuestion) QuestionType() string { return typeNoul }

// MarshalJSON writes {"type":"noul","instructions":<entry>[,"criteria":{...}]}.
//
// "instructions" is always present and is null when unset: the official SDK
// defaults it to null rather than omitting it, so the key is written
// unconditionally. "criteria" is omitted when nil — never null, which the
// service rejects with HTTP 400.
func (q NoulQuestion) MarshalJSON() ([]byte, error) {
	type wire struct {
		Type         string        `json:"type"`
		Instructions Entry         `json:"instructions"`
		Criteria     *NoulCriteria `json:"criteria,omitempty"`
	}
	return marshalNoHTMLEscape(wire{Type: typeNoul, Instructions: q.Instructions, Criteria: q.Criteria})
}

// ---------------------------------------------------------------------------
// Choice
// ---------------------------------------------------------------------------

// ChoiceQuestion picks one of several named alternatives. The answer names the
// selected label and carries a probability for every label.
type ChoiceQuestion struct {
	// Instructions is the question itself. Always written, null when unset.
	Instructions Entry `json:"instructions"`

	// Criteria maps each label to its description. A nil value leaves the label
	// undescribed.
	//
	// Between one and maxChoiceOptions (255) labels are required; an empty or
	// nil map is rejected by the service with HTTP 400 ("Choice question must
	// have at least one choice"). See ValidateQuestions, which enforces both
	// bounds before the request is sent.
	Criteria map[string]Entry `json:"criteria"`
}

func (ChoiceQuestion) isQuestion() {}

// QuestionType implements Question.
func (ChoiceQuestion) QuestionType() string { return typeChoice }

// MarshalJSON writes {"type":"choice","instructions":<entry>,"criteria":{...}}.
//
// A map value of nil becomes JSON null, which the service reads as an
// undescribed label.
func (q ChoiceQuestion) MarshalJSON() ([]byte, error) {
	type wire struct {
		Type         string           `json:"type"`
		Instructions Entry            `json:"instructions"`
		Criteria     map[string]Entry `json:"criteria"`
	}
	return marshalNoHTMLEscape(wire{Type: typeChoice, Instructions: q.Instructions, Criteria: q.Criteria})
}

// ---------------------------------------------------------------------------
// Score
// ---------------------------------------------------------------------------

// ScoreQuestion places the state on an ordered rubric. Criteria are indexed by
// score from zero; entries may be nil.
//
// The answer is an expected score, which may fall between rubric levels, plus a
// probability for each level.
type ScoreQuestion struct {
	// Instructions is the question itself. Always written, null when unset.
	Instructions Entry `json:"instructions"`

	// Criteria are the ordered rubric levels. Written as a JSON array, never a
	// map.
	//
	// The API documents between minScoreLevels (2) and maxScoreLevels (10)
	// levels; see ValidateQuestions for why the minimum is not relaxed to match
	// hosts that accept one.
	Criteria []Entry `json:"criteria"`
}

func (ScoreQuestion) isQuestion() {}

// QuestionType implements Question.
func (ScoreQuestion) QuestionType() string { return typeScore }

// MarshalJSON writes {"type":"score","instructions":<entry>,"criteria":[...]}.
func (q ScoreQuestion) MarshalJSON() ([]byte, error) {
	type wire struct {
		Type         string  `json:"type"`
		Instructions Entry   `json:"instructions"`
		Criteria     []Entry `json:"criteria"`
	}
	return marshalNoHTMLEscape(wire{Type: typeScore, Instructions: q.Instructions, Criteria: q.Criteria})
}

// ---------------------------------------------------------------------------
// Question sets
// ---------------------------------------------------------------------------

// Questions maps the name of each question to the question. The name is the key
// under which the answer comes back, so Result accessors take the same string.
type Questions map[string]Question

// ValidateQuestions rejects question sets the service would reject.
//
// It mirrors the official SDK's validateQuestions: the set must not be empty, a
// Score question needs at least two criteria, and a Choice question needs at
// least one label. It also enforces the two upper bounds the API documents: a
// Score rubric accepts at most maxScoreLevels (10) levels, and a Choice accepts
// at most maxChoiceOptions (255) options.
//
// A Noul is checked too, and the rule is all or nothing: criteria may be omitted
// entirely (NoulQuestion.Criteria == nil), but when it is present both the true
// and the false side must be non-nil. The live endpoint rejects a null side, or
// only one side, with HTTP 400 — see validateNoulCriteria for the recorded
// exchanges. Note that a nil side is never something the client can send
// accidentally-but-legally: the marshaller always writes an explicit null, so a
// half-filled criteria is a guaranteed failure.
//
// Two further checks in the TypeScript implementation have no Go counterpart
// because the type system makes them unrepresentable at compile time:
// ChoiceQuestion.Criteria is a map[string]Entry and ScoreQuestion.Criteria is a
// []Entry, so criteria can never be "a list where a map belongs" or the reverse.
//
// The Score minimum is deliberate, and not a bug to "fix". The API documentation
// says a Score has at least two levels, and the official TS SDK requires two;
// some hosts validate more loosely and accept one (an observed gateway rejects
// only zero levels). A single level carries no ordering and means the same thing
// as a Noul, so this client stays with the documented minimum and asks for a
// NoulQuestion instead. Do not relax it to match a host that is more permissive
// than the API it proxies.
//
// An unknown question type cannot be constructed outside this package (the
// Question interface is sealed), and a nil question is rejected defensively.
//
// Every error wraps ErrValidation, because the caller's request is what is
// wrong: errors.Is(err, ErrValidation) reports a question set that was never
// worth sending, and Client.Evaluate returns the error before any network call.
func ValidateQuestions(q Questions) error {
	if len(q) == 0 {
		return fmt.Errorf("%w: at least one question is required", ErrValidation)
	}

	// Validate in name order so that a caller with several broken questions
	// always sees the same first failure.
	names := make([]string, 0, len(q))
	for name := range q {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		switch question := q[name].(type) {
		case NoulQuestion:
			if err := validateNoulCriteria(name, question.Criteria); err != nil {
				return err
			}

		case *NoulQuestion:
			if question == nil {
				return nilQuestionError(name)
			}
			if err := validateNoulCriteria(name, question.Criteria); err != nil {
				return err
			}

		case ChoiceQuestion:
			if err := validateChoiceCriteria(name, question.Criteria); err != nil {
				return err
			}

		case *ChoiceQuestion:
			if question == nil {
				return nilQuestionError(name)
			}
			if err := validateChoiceCriteria(name, question.Criteria); err != nil {
				return err
			}

		case ScoreQuestion:
			if err := validateScoreCriteria(name, question.Criteria); err != nil {
				return err
			}

		case *ScoreQuestion:
			if question == nil {
				return nilQuestionError(name)
			}
			if err := validateScoreCriteria(name, question.Criteria); err != nil {
				return err
			}

		case nil:
			return nilQuestionError(name)

		default:
			return fmt.Errorf("%w: question %q has unknown type %q", ErrValidation, name, question.QuestionType())
		}
	}
	return nil
}

func nilQuestionError(name string) error {
	return fmt.Errorf("%w: question %q is nil", ErrValidation, name)
}

// validateNoulCriteria enforces the all-or-nothing rule the live endpoint
// exposed: a noul's criteria object is optional, but once it is present BOTH
// sides must carry a non-null entry.
//
// Verified against the endpoint, with the raw exchange recorded in the task-4
// report:
//
//	criteria omitted entirely     -> HTTP 200
//	{"true":"y","false":"n"}      -> HTTP 200
//	{"true":"y","false":null}     -> HTTP 400 (path questions.q.criteria.false)
//	{"true":null,"false":"n"}     -> HTTP 400 (path questions.q.criteria.true)
//	{"true":null,"false":null}    -> HTTP 400
//	{"true":"y"}                  -> HTTP 400 (criteria.false is required)
//
// The marshaller cannot express "one side only": NoulCriteria.True and .False
// are plain Entry fields, so a nil side always serializes to an explicit null.
// That leaves the request layer with exactly one legal non-nil shape, and a
// half-filled struct is the natural mistake — it reads as "I only care about
// the true side". Catching it here turns an opaque zod message into a local
// error that says what to write instead.
//
// A nil criteria pointer stays valid: that is the common shape, and the key is
// then omitted from the request entirely.
func validateNoulCriteria(name string, criteria *NoulCriteria) error {
	if criteria == nil {
		return nil
	}
	if criteria.True == nil {
		return fmt.Errorf(
			"%w: noul question %q has a null \"true\" criterion; a noul's criteria is all or "+
				"nothing — either omit criteria entirely, or give both outcomes without null",
			ErrValidation, name)
	}
	if criteria.False == nil {
		return fmt.Errorf(
			"%w: noul question %q has a null \"false\" criterion; a noul's criteria is all or "+
				"nothing — either omit criteria entirely, or give both outcomes without null",
			ErrValidation, name)
	}
	return nil
}

// validateScoreCriteria enforces the documented rubric size: at least two levels
// and at most ten. The minimum is deliberately not relaxed to match hosts that
// accept one level; see ValidateQuestions.
func validateScoreCriteria(name string, criteria []Entry) error {
	if len(criteria) < minScoreLevels {
		return fmt.Errorf(
			"%w: score question %q has %d criteria; at least two scores are required "+
				"(a one-level rubric has no ordering and should be a noul question)",
			ErrValidation, name, len(criteria))
	}
	if len(criteria) > maxScoreLevels {
		return fmt.Errorf(
			"%w: score question %q has %d criteria; the API accepts at most %d levels",
			ErrValidation, name, len(criteria), maxScoreLevels)
	}
	return nil
}

// validateChoiceCriteria enforces the documented option count: at least one
// label, at most 255. A choice with no labels is rejected with HTTP 400 by the
// service, so this saves a request that cannot succeed.
func validateChoiceCriteria(name string, criteria map[string]Entry) error {
	if len(criteria) == 0 {
		return fmt.Errorf(
			"%w: choice question %q has no criteria; at least one choice is required",
			ErrValidation, name)
	}
	if len(criteria) > maxChoiceOptions {
		return fmt.Errorf(
			"%w: choice question %q has %d criteria; the API accepts at most %d options",
			ErrValidation, name, len(criteria), maxChoiceOptions)
	}
	return nil
}

// marshalQuestions encodes a question set as the "questions" member of a request
// body.
//
// Map keys are emitted in sorted order, which is what encoding/json does for
// maps and therefore deterministic, but differs from the TypeScript SDK's
// insertion order. The resulting JSON object is equivalent; only the key order
// inside the object differs.
//
// It performs no validation: call ValidateQuestions first, as Client.Evaluate
// does. Keeping the two apart lets a caller inspect exactly which question
// bodies would go on the wire.
func marshalQuestions(q Questions) ([]byte, error) {
	return marshalNoHTMLEscape(q)
}

// marshalNoHTMLEscape encodes v the way JavaScript's JSON.stringify does:
// compact, with "<", ">" and "&" left literal.
//
// encoding/json escapes those three by default, a Go-specific defence against
// injecting markup into HTML responses. This API returns JSON and the reference
// implementation is a JavaScript client, so the escaping is pure overhead here:
// it inflates the body for any prompt containing markup and makes byte-level
// comparison against the reference client misleading. Both spellings decode to
// the same value, which is exactly why this has to be deliberate rather than
// left to chance.
//
// Every request byte goes through here — the question map, the state, and the
// enclosing payload — so there is one place where the encoding policy lives. The
// whole package is one file away from being inconsistent about it otherwise.
func marshalNoHTMLEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
