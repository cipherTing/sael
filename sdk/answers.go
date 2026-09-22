package sdk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// Usage reports token accounting for one request.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`

	// Cost is present only on hosts that report billing per request; it is nil
	// when the host reports token counts alone. A pointer keeps "not reported"
	// distinct from a reported cost of zero.
	Cost *float64 `json:"cost,omitempty"`
}

// Answer is one typed answer. The interface is sealed by the unexported isAnswer
// method: the only implementations are NoulAnswer, ChoiceAnswer and ScoreAnswer.
type Answer interface {
	isAnswer()

	// AnswerType is the wire discriminator: "noul", "choice" or "score".
	AnswerType() string
}

// ---------------------------------------------------------------------------
// Answers
// ---------------------------------------------------------------------------

// NoulAnswer is the probability that the Noul question was answered "true", in
// [0,1].
type NoulAnswer struct {
	Noul float64
}

func (NoulAnswer) isAnswer() {}

// AnswerType implements Answer.
func (NoulAnswer) AnswerType() string { return typeNoul }

// ChoiceAnswer is the selected label with a probability for every label.
type ChoiceAnswer struct {
	// Choice is the selected label. It is one of the keys of the question's
	// criteria.
	Choice string

	// Confidence is the reported confidence in the selection, in [0,1].
	Confidence float64

	// Probabilities is keyed by label. Choice labels are arbitrary strings, so
	// unlike a Score rubric they stay a map.
	Probabilities map[string]float64
}

func (ChoiceAnswer) isAnswer() {}

// AnswerType implements Answer.
func (ChoiceAnswer) AnswerType() string { return typeChoice }

// ScoreAnswer places the state on the rubric.
//
// Legend and Probabilities are indexed by score: Legend[i] describes score i and
// Probabilities[i] is the probability mass at score i. The wire format keys both
// by the decimal score as a string; the decoder densifies them into slices.
type ScoreAnswer struct {
	// Score is the expected score. It may fall between rubric levels — the live
	// sample in doc.go reports 0.25 for a four-level rubric — so it is never
	// rounded into an index into Legend.
	Score float64

	// Confidence is the reported confidence in the score, in [0,1].
	Confidence float64

	// Legend holds the rubric descriptions indexed by score. A level missing
	// from the response is an empty string.
	Legend []string

	// Probabilities holds the probability of each score, indexed the same way. A
	// level missing from the response is zero.
	Probabilities []float64
}

func (ScoreAnswer) isAnswer() {}

// AnswerType implements Answer.
func (ScoreAnswer) AnswerType() string { return typeScore }

// ---------------------------------------------------------------------------
// Result
// ---------------------------------------------------------------------------

// Result is one successful response.
//
// Model is the resolved, versioned model id, which may differ from the string
// requested (an alias such as "jev-latest" resolves to a pinned version): log it,
// because answer behaviour is tied to that version. Provider and ID are optional
// metadata: a host may omit either or both, and they then stay empty rather than
// failing the decode.
type Result struct {
	Model    string
	Provider string
	ID       string
	Usage    Usage

	// Answers holds every answer the service returned, keyed by question name.
	// An answer whose type this client does not understand is skipped, so the
	// rest still decode.
	Answers map[string]Answer
}

// Noul returns the answer stored under name if it is a noul answer. The bool is
// false when the name is absent, the answer has a different type, or r is nil.
func (r *Result) Noul(name string) (NoulAnswer, bool) {
	a, ok := r.answer(name)
	if !ok {
		return NoulAnswer{}, false
	}
	switch v := a.(type) {
	case NoulAnswer:
		return v, true
	case *NoulAnswer:
		// decodeResult always stores values; pointers are accepted so that
		// hand-built Results behave the same way.
		if v == nil {
			return NoulAnswer{}, false
		}
		return *v, true
	default:
		return NoulAnswer{}, false
	}
}

// Choice returns the answer stored under name if it is a choice answer. The bool
// is false when the name is absent, the answer has a different type, or r is nil.
func (r *Result) Choice(name string) (ChoiceAnswer, bool) {
	a, ok := r.answer(name)
	if !ok {
		return ChoiceAnswer{}, false
	}
	switch v := a.(type) {
	case ChoiceAnswer:
		return v, true
	case *ChoiceAnswer:
		if v == nil {
			return ChoiceAnswer{}, false
		}
		return *v, true
	default:
		return ChoiceAnswer{}, false
	}
}

// Score returns the answer stored under name if it is a score answer. The bool
// is false when the name is absent, the answer has a different type, or r is nil.
func (r *Result) Score(name string) (ScoreAnswer, bool) {
	a, ok := r.answer(name)
	if !ok {
		return ScoreAnswer{}, false
	}
	switch v := a.(type) {
	case ScoreAnswer:
		return v, true
	case *ScoreAnswer:
		if v == nil {
			return ScoreAnswer{}, false
		}
		return *v, true
	default:
		return ScoreAnswer{}, false
	}
}

func (r *Result) answer(name string) (Answer, bool) {
	if r == nil {
		return nil, false
	}
	a, ok := r.Answers[name]
	if !ok || a == nil {
		return nil, false
	}
	return a, true
}

// ---------------------------------------------------------------------------
// Decoding
// ---------------------------------------------------------------------------

// wireResult mirrors the success response body. Answers are kept raw because the
// type discriminator has to be read before an answer can be decoded.
type wireResult struct {
	Model    string                     `json:"model"`
	Provider string                     `json:"provider"`
	ID       string                     `json:"id"`
	Usage    Usage                      `json:"usage"`
	Answers  map[string]json.RawMessage `json:"answers"`
}

// decodeResult decodes a 2xx response body.
//
// Error responses never reach this function: Client.Evaluate turns a non-2xx
// status into an APIError. An answer carrying an unknown "type" is skipped
// rather than reported as an error, so a server that adds a new answer kind does
// not take down the answers a caller does understand. Errors are returned only
// for bytes that are not the documented shape.
func decodeResult(b []byte) (*Result, error) {
	var wire wireResult
	if err := json.Unmarshal(b, &wire); err != nil {
		return nil, fmt.Errorf("sdk: invalid response body: %w", err)
	}

	result := &Result{
		Model:    wire.Model,
		Provider: wire.Provider,
		ID:       wire.ID,
		Usage:    wire.Usage,
		Answers:  make(map[string]Answer, len(wire.Answers)),
	}

	for name, raw := range wire.Answers {
		answer, err := decodeAnswer(raw)
		if err != nil {
			return nil, fmt.Errorf("sdk: answer %q: %w", name, err)
		}
		if answer == nil {
			continue // unknown answer type: skipped, the rest are kept
		}
		result.Answers[name] = answer
	}
	return result, nil
}

// decodeAnswer decodes one entry of "answers". It returns (nil, nil) for an
// unknown or missing "type", which the caller reads as "skip this answer".
func decodeAnswer(raw json.RawMessage) (Answer, error) {
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, fmt.Errorf("invalid answer object: %w", err)
	}

	switch header.Type {
	case typeNoul:
		var wire struct {
			Noul float64 `json:"noul"`
		}
		if err := json.Unmarshal(raw, &wire); err != nil {
			return nil, fmt.Errorf("invalid noul answer: %w", err)
		}
		return NoulAnswer{Noul: wire.Noul}, nil

	case typeChoice:
		var wire struct {
			Choice        string             `json:"choice"`
			Confidence    float64            `json:"confidence"`
			Probabilities map[string]float64 `json:"probabilities"`
		}
		if err := json.Unmarshal(raw, &wire); err != nil {
			return nil, fmt.Errorf("invalid choice answer: %w", err)
		}
		return ChoiceAnswer{
			Choice:        wire.Choice,
			Confidence:    wire.Confidence,
			Probabilities: wire.Probabilities,
		}, nil

	case typeScore:
		// legend and probabilities are objects keyed by the decimal score as a
		// string, so they are read raw and then densified into slices.
		var wire struct {
			Score         float64                    `json:"score"`
			Confidence    float64                    `json:"confidence"`
			Legend        map[string]json.RawMessage `json:"legend"`
			Probabilities map[string]json.RawMessage `json:"probabilities"`
		}
		if err := json.Unmarshal(raw, &wire); err != nil {
			return nil, fmt.Errorf("invalid score answer: %w", err)
		}
		probabilities, err := decodeScoreProbabilities(wire.Probabilities)
		if err != nil {
			return nil, err
		}
		return ScoreAnswer{
			Score:         wire.Score,
			Confidence:    wire.Confidence,
			Legend:        decodeScoreLegend(wire.Legend),
			Probabilities: probabilities,
		}, nil

	default:
		return nil, nil
	}
}

// scoreEntry is one key/value pair of a score-keyed object, kept with its
// original key so that error messages can name it.
type scoreEntry struct {
	index int
	key   string
	value json.RawMessage
}

// scoreEntries parses the keys of a score-keyed object and returns the valid
// ones ordered by key, so that decoding is deterministic even when several keys
// map to the same index (for example "1" and "01").
//
// A key that is not a plain non-negative decimal integer, or that is above
// maxScoreIndex, is ignored.
func scoreEntries(m map[string]json.RawMessage) []scoreEntry {
	entries := make([]scoreEntry, 0, len(m))
	for key, value := range m {
		index, ok := parseScoreKey(key)
		if !ok {
			continue
		}
		entries = append(entries, scoreEntry{index: index, key: key, value: value})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	return entries
}

// parseScoreKey accepts only what strconv.Atoi would accept without a sign:
// digits, nothing else. "1.5", "+1", "-1", " 1" and "" are all rejected.
func parseScoreKey(key string) (int, bool) {
	if key == "" || len(key) > 10 {
		return 0, false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return 0, false
		}
	}
	index, err := strconv.Atoi(key)
	if err != nil || index > maxScoreIndex {
		return 0, false
	}
	return index, true
}

// decodeScoreLegend densifies {"0":"none","2":"serious"} into a slice indexed by
// score, with "" for any level the response omitted. The slice length is the
// highest valid index plus one; an empty or entirely invalid object yields nil.
func decodeScoreLegend(m map[string]json.RawMessage) []string {
	entries := scoreEntries(m)
	if len(entries) == 0 {
		return nil
	}
	legend := make([]string, scoreLength(entries))
	for _, entry := range entries {
		legend[entry.index] = legendEntry(entry.value)
	}
	return legend
}

// decodeScoreProbabilities densifies a score-keyed probabilities object the same
// way, with 0 for any level the response omitted.
//
// A probability that is not a JSON number is an error: unlike an unrecognised
// key, it means the response cannot be interpreted.
func decodeScoreProbabilities(m map[string]json.RawMessage) ([]float64, error) {
	entries := scoreEntries(m)
	if len(entries) == 0 {
		return nil, nil
	}
	probabilities := make([]float64, scoreLength(entries))
	for _, entry := range entries {
		var value float64
		if err := json.Unmarshal(entry.value, &value); err != nil {
			return nil, fmt.Errorf("sdk: score probability %q is not a number: %w", entry.key, err)
		}
		probabilities[entry.index] = value
	}
	return probabilities, nil
}

// scoreLength is the slice length implied by the valid entries: the highest index
// plus one.
func scoreLength(entries []scoreEntry) int {
	length := 0
	for _, entry := range entries {
		if entry.index+1 > length {
			length = entry.index + 1
		}
	}
	return length
}

// legendEntry renders one rubric description. A JSON null is an undescribed
// level and becomes ""; a string keeps its value. Score criteria are documented
// as entries, which may also be a JSON object or array, so anything else is kept
// as its raw JSON text rather than dropped.
func legendEntry(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err == nil {
			return s
		}
	}
	return string(trimmed)
}
