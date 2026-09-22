package sdk

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// liveResponse is the response sample recorded in doc.go, reached through a
// gateway, so it carries the gateway-added "provider", "id" and "usage.cost".
const liveResponse = `{
  "model": "typesafe/jev-1.13-20260917",
  "provider": "TypeSafe",
  "id": "gen-dec-1789963946-abc123",
  "usage": {"input_tokens": 405, "output_tokens": 68, "cost": 1.701e-05},
  "answers": {
    "harmful": {"type": "noul", "noul": 0.73},
    "topic": {
      "type": "choice",
      "choice": "billing",
      "probabilities": {"other": 0, "tech": 0, "billing": 1},
      "confidence": 1
    },
    "severity": {
      "type": "score",
      "score": 0.25,
      "legend": {"0": "none", "1": "mild", "2": "serious", "3": "severe"},
      "probabilities": {"0": 0.76, "1": 0.24, "2": 0, "3": 0},
      "confidence": 0.75
    }
  }
}`

func TestDecodeResultLiveSample(t *testing.T) {
	result, err := decodeResult([]byte(liveResponse))
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "typesafe/jev-1.13-20260917", result.Model)
	assert.Equal(t, "TypeSafe", result.Provider)
	assert.Equal(t, "gen-dec-1789963946-abc123", result.ID)
	assert.Equal(t, 405, result.Usage.InputTokens)
	assert.Equal(t, 68, result.Usage.OutputTokens)
	require.NotNil(t, result.Usage.Cost)
	assert.Equal(t, 1.701e-05, *result.Usage.Cost)

	require.Len(t, result.Answers, 3)

	noul, ok := result.Noul("harmful")
	require.True(t, ok, "harmful should be a noul answer")
	assert.Equal(t, 0.73, noul.Noul)

	choice, ok := result.Choice("topic")
	require.True(t, ok, "topic should be a choice answer")
	assert.Equal(t, "billing", choice.Choice)
	assert.Equal(t, 1.0, choice.Confidence)
	assert.Equal(t, map[string]float64{"other": 0, "tech": 0, "billing": 1}, choice.Probabilities)

	score, ok := result.Score("severity")
	require.True(t, ok, "severity should be a score answer")
	// The expectation is fractional even though the rubric is discrete: it must
	// not be rounded into an index.
	assert.Equal(t, 0.25, score.Score)
	assert.Equal(t, 0.75, score.Confidence)
	assert.Equal(t, []string{"none", "mild", "serious", "severe"}, score.Legend)
	assert.Equal(t, []float64{0.76, 0.24, 0, 0}, score.Probabilities)
}

func TestDecodeResultMinimalOfficialResponse(t *testing.T) {
	// The official host sends no provider, no id and no cost. All three must
	// stay zero rather than failing the decode.
	body := `{
	  "model": "jev-1.13.0",
	  "usage": {"input_tokens": 12, "output_tokens": 4},
	  "answers": {"harmful": {"type": "noul", "noul": 0.1}}
	}`

	result, err := decodeResult([]byte(body))
	require.NoError(t, err)
	assert.Equal(t, "jev-1.13.0", result.Model)
	assert.Empty(t, result.Provider)
	assert.Empty(t, result.ID)
	assert.Equal(t, 12, result.Usage.InputTokens)
	assert.Equal(t, 4, result.Usage.OutputTokens)
	assert.Nil(t, result.Usage.Cost)

	noul, ok := result.Noul("harmful")
	require.True(t, ok)
	assert.Equal(t, 0.1, noul.Noul)
}

func TestDecodeResultWithoutUsageOrAnswers(t *testing.T) {
	result, err := decodeResult([]byte(`{"model": "jev-1.13.0"}`))
	require.NoError(t, err)
	assert.Equal(t, "jev-1.13.0", result.Model)
	assert.Equal(t, Usage{}, result.Usage)
	assert.NotNil(t, result.Answers)
	assert.Empty(t, result.Answers)
}

func TestDecodeResultInvalidJSON(t *testing.T) {
	result, err := decodeResult([]byte(`{"model": `))
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid response body")
}

func TestDecodeUnknownAnswerTypeIsSkipped(t *testing.T) {
	// A new answer kind must not be able to take down the answers a caller does
	// understand, and it must not panic.
	body := `{
	  "model": "jev-1.13.0",
	  "answers": {
	    "harmful": {"type": "noul", "noul": 0.4},
	    "future": {"type": "ranking", "ranking": [1, 2, 3]},
	    "typeless": {"noul": 0.9}
	  }
	}`

	result, err := decodeResult([]byte(body))
	require.NoError(t, err)
	require.Len(t, result.Answers, 1)

	noul, ok := result.Noul("harmful")
	require.True(t, ok)
	assert.Equal(t, 0.4, noul.Noul)

	_, ok = result.Noul("future")
	assert.False(t, ok)
	_, ok = result.Noul("typeless")
	assert.False(t, ok)
}

func TestDecodeUsageCost(t *testing.T) {
	tests := []struct {
		name     string
		usage    string
		wantCost *float64
	}{
		{name: "gateway reports a cost", usage: `{"input_tokens":1,"output_tokens":2,"cost":1.701e-05}`, wantCost: float64Ptr(1.701e-05)},
		{name: "official host omits cost", usage: `{"input_tokens":1,"output_tokens":2}`},
		{name: "explicit null cost", usage: `{"input_tokens":1,"output_tokens":2,"cost":null}`},
		{name: "no usage at all", usage: ``},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"model":"m","answers":{}`
			if tt.usage != "" {
				body += `,"usage":` + tt.usage
			}
			body += `}`

			result, err := decodeResult([]byte(body))
			require.NoError(t, err)
			if tt.wantCost == nil {
				assert.Nil(t, result.Usage.Cost)
				return
			}
			require.NotNil(t, result.Usage.Cost)
			assert.Equal(t, *tt.wantCost, *result.Usage.Cost)
		})
	}
}

func TestDecodeScoreAnswerDensifiesStringKeys(t *testing.T) {
	tests := []struct {
		name          string
		answer        string
		wantLegend    []string
		wantProb      []float64
		wantScore     float64
		wantConfident float64
	}{
		{
			name:          "keys out of order",
			answer:        `{"type":"score","score":1.5,"legend":{"3":"severe","0":"none","2":"serious","1":"mild"},"probabilities":{"3":0.1,"0":0.4,"2":0.3,"1":0.2},"confidence":0.5}`,
			wantLegend:    []string{"none", "mild", "serious", "severe"},
			wantProb:      []float64{0.4, 0.2, 0.3, 0.1},
			wantScore:     1.5,
			wantConfident: 0.5,
		},
		{
			name:       "missing middle key is zero filled",
			answer:     `{"type":"score","score":2,"legend":{"0":"none","2":"serious"},"probabilities":{"0":0.5,"2":0.5}}`,
			wantLegend: []string{"none", "", "serious"},
			wantProb:   []float64{0.5, 0, 0.5},
			wantScore:  2,
		},
		{
			name:       "non contiguous keys leave gaps",
			answer:     `{"type":"score","score":5,"legend":{"0":"none","5":"worst"},"probabilities":{"0":0.9,"5":0.1}}`,
			wantLegend: []string{"none", "", "", "", "", "worst"},
			wantProb:   []float64{0.9, 0, 0, 0, 0, 0.1},
			wantScore:  5,
		},
		{
			name:       "empty objects yield empty slices",
			answer:     `{"type":"score","score":0,"legend":{},"probabilities":{}}`,
			wantLegend: nil,
			wantProb:   nil,
		},
		{
			name:       "absent legend and probabilities",
			answer:     `{"type":"score","score":1}`,
			wantLegend: nil,
			wantProb:   nil,
			wantScore:  1,
		},
		{
			name:       "invalid keys are ignored",
			answer:     `{"type":"score","score":1,"legend":{"0":"none","x":"bad","-1":"neg","1.5":"frac","+2":"plus"," 3":"space","1":"mild"},"probabilities":{"0":0.25,"x":0.99,"1":0.75}}`,
			wantLegend: []string{"none", "mild"},
			wantProb:   []float64{0.25, 0.75},
			wantScore:  1,
		},
		{
			name:       "absurd index is ignored instead of allocating",
			answer:     `{"type":"score","score":1,"legend":{"0":"none","1":"mild","999999999":"huge"},"probabilities":{"0":1,"1":0}}`,
			wantLegend: []string{"none", "mild"},
			wantProb:   []float64{1, 0},
			wantScore:  1,
		},
		{
			name:       "null legend entry becomes an empty string",
			answer:     `{"type":"score","score":1,"legend":{"0":null,"1":"mild"},"probabilities":{"0":0,"1":1}}`,
			wantLegend: []string{"", "mild"},
			wantProb:   []float64{0, 1},
			wantScore:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"model":"m","answers":{"severity":` + tt.answer + `}}`
			result, err := decodeResult([]byte(body))
			require.NoError(t, err)

			score, ok := result.Score("severity")
			require.True(t, ok)
			assert.Equal(t, tt.wantScore, score.Score)
			assert.Equal(t, tt.wantConfident, score.Confidence)
			assert.Equal(t, tt.wantLegend, score.Legend)
			assert.Equal(t, tt.wantProb, score.Probabilities)
		})
	}
}

func TestDecodeScoreProbabilityMustBeNumber(t *testing.T) {
	body := `{"model":"m","answers":{"severity":` +
		`{"type":"score","score":1,"legend":{"0":"a","1":"b"},"probabilities":{"0":"zero","1":1}}` +
		`}}`
	result, err := decodeResult([]byte(body))
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), `score probability "0" is not a number`)
}

func TestAnswerAccessors(t *testing.T) {
	result := &Result{
		Answers: map[string]Answer{
			"harmful":  NoulAnswer{Noul: 0.8},
			"topic":    ChoiceAnswer{Choice: "tech", Confidence: 0.9, Probabilities: map[string]float64{"tech": 0.9}},
			"severity": ScoreAnswer{Score: 2.5, Legend: []string{"none", "mild", "serious"}},
			"pointer":  &NoulAnswer{Noul: 0.2},
		},
	}

	noul, ok := result.Noul("harmful")
	require.True(t, ok)
	assert.Equal(t, 0.8, noul.Noul)

	choice, ok := result.Choice("topic")
	require.True(t, ok)
	assert.Equal(t, "tech", choice.Choice)
	assert.Equal(t, 0.9, choice.Confidence)

	score, ok := result.Score("severity")
	require.True(t, ok)
	assert.Equal(t, 2.5, score.Score)
	assert.Equal(t, []string{"none", "mild", "serious"}, score.Legend)

	noul, ok = result.Noul("pointer")
	require.True(t, ok)
	assert.Equal(t, 0.2, noul.Noul)

	// Name does not exist.
	_, ok = result.Noul("missing")
	assert.False(t, ok)
	_, ok = result.Choice("missing")
	assert.False(t, ok)
	_, ok = result.Score("missing")
	assert.False(t, ok)

	// Name exists but carries another answer type.
	_, ok = result.Choice("harmful")
	assert.False(t, ok, "a noul answer must not satisfy Choice")
	_, ok = result.Score("harmful")
	assert.False(t, ok, "a noul answer must not satisfy Score")
	_, ok = result.Noul("topic")
	assert.False(t, ok, "a choice answer must not satisfy Noul")
	_, ok = result.Score("topic")
	assert.False(t, ok, "a choice answer must not satisfy Score")
	_, ok = result.Noul("severity")
	assert.False(t, ok, "a score answer must not satisfy Noul")
	_, ok = result.Choice("severity")
	assert.False(t, ok, "a score answer must not satisfy Choice")
}

func TestAnswerAccessorsNilResult(t *testing.T) {
	var result *Result

	_, ok := result.Noul("harmful")
	assert.False(t, ok)
	_, ok = result.Choice("topic")
	assert.False(t, ok)
	_, ok = result.Score("severity")
	assert.False(t, ok)
}

func TestAnswerAccessorsNilMap(t *testing.T) {
	result := &Result{}

	_, ok := result.Noul("harmful")
	assert.False(t, ok)
	_, ok = result.Choice("topic")
	assert.False(t, ok)
	_, ok = result.Score("severity")
	assert.False(t, ok)
}

func TestAnswerTypes(t *testing.T) {
	var answers = []struct {
		answer Answer
		want   string
	}{
		{NoulAnswer{}, "noul"},
		{&NoulAnswer{}, "noul"},
		{ChoiceAnswer{}, "choice"},
		{ScoreAnswer{}, "score"},
	}
	for _, tt := range answers {
		assert.Equal(t, tt.want, tt.answer.AnswerType())
	}
}

func float64Ptr(v float64) *float64 { return &v }
