package cli

// output_test.go covers the two renderings and the rule that chooses between
// them.
//
// The outputs are compared as exact text rather than by probing for substrings.
// That is deliberate: the JSON is a contract other programs parse, and the human
// view is what a person reads to decide whether anything fired. Pinning the whole
// document catches a renamed key or a reordered section, which a substring check
// would sail past.
//
// Nothing here touches the network. writeReport and writeJSON take a Result, so
// they are tested directly with a buffer standing in for the terminal.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cipherTing/sael/cli/internal/questions"
	"github.com/cipherTing/sael/sdk"
)

// ---------------------------------------------------------------------------
// Ranking
// ---------------------------------------------------------------------------

func TestRankedHazardsOrdersByStrengthThenName(t *testing.T) {
	result := newResult(map[string]sdk.Answer{
		"delta": sdk.NoulAnswer{Noul: 0.10},
		"alpha": sdk.NoulAnswer{Noul: 0.90},
		"gamma": sdk.NoulAnswer{Noul: 0.50},
		// Two questions can land on the same probability and a map has no order,
		// so the tie needs a stable tiebreak or the ranking shuffles between runs.
		"bravo": sdk.NoulAnswer{Noul: 0.50},
		"echo":  sdk.NoulAnswer{Noul: 0.00},
	})

	assert.Equal(t, []string{"alpha", "bravo", "gamma", "delta", "echo"}, hazardNames(rankedHazards(result)),
		"the ranking is the whole point of the human view: the first line has to be "+
			"the answer that fired hardest, and a tie has to resolve the same way twice")
}

func TestRankedHazardsIgnoresAnswersThatAreNotYesOrNo(t *testing.T) {
	result := newResult(map[string]sdk.Answer{
		"a_noul":  sdk.NoulAnswer{Noul: 0.80},
		"a_score": sdk.ScoreAnswer{Score: 3, Confidence: 1, Legend: []string{"a", "b", "c", "d"}},
		"a_choice": sdk.ChoiceAnswer{
			Choice:        "yes",
			Confidence:    0.9,
			Probabilities: map[string]float64{"yes": 0.9, "no": 0.1},
		},
	})

	// A Score is a position on a scale and a Choice is a label. Neither is a
	// probability, and ranking one against a Noul would compare numbers that mean
	// different things.
	assert.Equal(t, []string{"a_noul"}, hazardNames(rankedHazards(result)))
}

func TestRankedHazardsOnAnEmptyResult(t *testing.T) {
	assert.Empty(t, rankedHazards(newResult(nil)))
}

func TestScaledAnswersAreSortedByName(t *testing.T) {
	result := newResult(map[string]sdk.Answer{
		"zeta":  sdk.ScoreAnswer{Score: 1, Legend: []string{"a", "b"}},
		"alpha": sdk.ScoreAnswer{Score: 1, Legend: []string{"a", "b"}},
		"noul":  sdk.NoulAnswer{Noul: 0.5},
	})

	assert.Equal(t, []string{"alpha", "zeta"}, scaledAnswers(result),
		"scales are listed in a fixed order, because a map iterates randomly and two "+
			"sections appearing in a different order each run looks like a bug")
}

// ---------------------------------------------------------------------------
// JSON: the shape a script parses
// ---------------------------------------------------------------------------

func TestWriteJSONMatchesTheDocumentedShape(t *testing.T) {
	var buf bytes.Buffer

	require.NoError(t, writeJSON(&buf, sampleResult()))

	assert.Equal(t, `[
  {
    "question": "alpha",
    "type": "noul",
    "value": 0.9
  },
  {
    "question": "beta",
    "type": "noul",
    "value": 0.4
  },
  {
    "question": "zzz",
    "type": "score",
    "value": 2.5,
    "scale": [
      "none",
      "mild",
      "serious",
      "severe"
    ],
    "confidence": 0.8
  }
]
`, buf.String(),
		"this JSON is a contract: a caller writes jq filters against these key names, "+
			"and a renamed or added field breaks them silently, so the whole document "+
			"is pinned rather than sampled")
}

// TestWriteJSONOmitsTheFieldsThatDoNotApply checks the absent half of the
// contract, which an exact match does not name clearly on its own.
//
// A noul carrying "scale": null would make every caller test for null before
// using it, and one that did not would fail far from here.
func TestWriteJSONOmitsTheFieldsThatDoNotApply(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, writeJSON(&buf, sampleResult()))

	var decoded []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &decoded))
	require.Len(t, decoded, 3)

	assert.Equal(t, []string{"question", "type", "value"}, sortedKeysOf(decoded[0]),
		"a noul answer carries no scale and no confidence")
	assert.Equal(t, []string{"question", "type", "value"}, sortedKeysOf(decoded[1]))
	assert.Equal(t, []string{"confidence", "question", "scale", "type", "value"}, sortedKeysOf(decoded[2]),
		"a scale answer carries the rubric and the reported confidence")
}

// TestWriteJSONAlwaysProducesAnArray guards the empty case, which is what a script
// hits on the path where nothing was reported.
//
// A nil slice encodes as JSON null, and `jq length` on null is an error rather
// than 0, so an empty answer set would break a pipeline instead of reporting
// nothing.
func TestWriteJSONAlwaysProducesAnArray(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, writeJSON(&buf, newResult(nil)))

	assert.Equal(t, "[]\n", buf.String())

	var decoded []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &decoded),
		"the output has to stay parseable when there is nothing to report")
	assert.Empty(t, decoded)
}

// TestWriteJSONIsDeterministic is the guard on map iteration.
//
// Answers is a map, so without the sort in rankedHazards the ranking would be a
// different permutation on every run — and a single run would still look
// plausible, which is what makes this worth repeating rather than checking once.
func TestWriteJSONIsDeterministic(t *testing.T) {
	result := sampleResult()

	var first string
	for i := 0; i < 100; i++ {
		var buf bytes.Buffer
		require.NoError(t, writeJSON(&buf, result))

		if i == 0 {
			first = buf.String()
			continue
		}
		require.Equal(t, first, buf.String(),
			"the JSON changed between runs on the same input; a caller diffing two "+
				"reports would see changes that did not happen")
	}
}

// TestWriteJSONLeavesNonASCIIAndMarkupAlone pins SetEscapeHTML(false).
//
// Go's encoder escapes <, > and & by default. That is an HTML defence and this
// output is not HTML: escaping turns a translated rubric into unreadable \u003c
// sequences and makes the report differ from what the model returned.
func TestWriteJSONLeavesNonASCIIAndMarkupAlone(t *testing.T) {
	result := newResult(map[string]sdk.Answer{
		"sexual": sdk.ScoreAnswer{
			Score:      1,
			Confidence: 0.5,
			Legend:     []string{"不含性内容", "a <b> and & mark"},
		},
	})

	var buf bytes.Buffer
	require.NoError(t, writeJSON(&buf, result))

	got := buf.String()
	assert.Contains(t, got, "不含性内容", "non-ASCII was escaped or mangled")
	assert.Contains(t, got, "a <b> and & mark", "markup was escaped")
	assert.NotContains(t, got, `\u003c`, "SetEscapeHTML is no longer in effect")
	assert.NotContains(t, got, `\u0026`)
}

func TestWriteJSONReportsAWriteFailure(t *testing.T) {
	err := writeJSON(failingWriter{}, sampleResult())

	require.Error(t, err, "a broken pipe has to surface rather than be swallowed")
	assert.Contains(t, err.Error(), "broken pipe")
}

// ---------------------------------------------------------------------------
// The human view
// ---------------------------------------------------------------------------

func TestWriteReportRanksDrawsAndNamesTheLevel(t *testing.T) {
	var buf bytes.Buffer

	require.NoError(t, writeReport(&buf, sampleResult(), false))

	assert.Equal(t, `  alpha  0.90  ██████████████████
  beta   0.40  ████████

  zzz    2.50/3  ███·  severe
`, buf.String(),
		"the terminal view is what a person reads to decide whether anything fired, "+
			"so its layout is worth pinning: the bars length out the value, the scale "+
			"draws one cell per level, and a bare 2.50 is only meaningful with the "+
			"name of the level it landed on beside it")
}

func TestWriteReportDrawsABarProportionalToTheValue(t *testing.T) {
	// The bar is the reading aid for the number beside it, so a bar that does not
	// track the value is worse than no bar: it would be trusted.
	cases := []struct {
		value float64
		want  int
	}{
		{0.00, 0},
		{0.02, 0},  // 0.4 rounds down
		{0.025, 1}, // exactly half a cell, and it rounds up into the first one
		{0.10, 2},
		{0.50, 10},
		{0.90, 18},
		{0.99, 20},
		{1.00, 20},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%.3f", tc.value), func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, writeReport(&buf, newResult(map[string]sdk.Answer{
				"x": sdk.NoulAnswer{Noul: tc.value},
			}), false))

			assert.Equal(t, tc.want, strings.Count(buf.String(), "█"),
				"a value of %.3f should draw %d of the %d cells", tc.value, tc.want, barWidth)
		})
	}
}

// TestWriteReportReadsTheRubricSizeFromTheAnswer is a regression guard.
//
// The scale's top was once a hardcoded 3.0, which is correct only for the question
// set that happened to exist then. A rubric of a different size then reported a
// total no level reached, so a bare "1.00/3" described a position on a scale that only
// had two levels.
func TestWriteReportReadsTheRubricSizeFromTheAnswer(t *testing.T) {
	cases := []struct {
		name   string
		score  float64
		legend []string
		want   string
	}{
		{"two levels", 1, []string{"no", "yes"}, "1.00/1"},
		{"four levels", 2, []string{"a", "b", "c", "d"}, "2.00/3"},
		{"ten levels, the API maximum", 5, []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}, "5.00/9"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, writeReport(&buf, newResult(map[string]sdk.Answer{
				"s": sdk.ScoreAnswer{Score: tc.score, Confidence: 1, Legend: tc.legend},
			}), false))

			assert.Contains(t, buf.String(), tc.want,
				"the denominator is the number of levels minus one, read from the "+
					"answer rather than assumed")
		})
	}
}

func TestWriteReportPutsAScaleOnlyReportOnOneLine(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, writeReport(&buf, newResult(map[string]sdk.Answer{
		"sexual": sdk.ScoreAnswer{Score: 1, Confidence: 1, Legend: []string{"none", "mild"}},
	}), false))

	assert.Equal(t, "  sexual  1.00/1  ██  mild\n", buf.String(),
		"the blank separator belongs between the two sections, so it must not appear "+
			"when there is only one")
}

func TestWriteReportWithNoAnswersSaysSo(t *testing.T) {
	var buf bytes.Buffer

	require.NoError(t, writeReport(&buf, newResult(nil), false))

	assert.Equal(t, "the response carried no answers\n", buf.String(),
		"printing nothing at all would look like the command failed silently")
}

func TestWriteReportLinesUpNamesOfDifferentLengths(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, writeReport(&buf, newResult(map[string]sdk.Answer{
		"a":                  sdk.NoulAnswer{Noul: 0.5},
		"a_much_longer_name": sdk.NoulAnswer{Noul: 0.4},
		"mid_length":         sdk.NoulAnswer{Noul: 0.3},
	}), false))

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	require.Len(t, lines, 3)

	// Every probability and every bar starts in the same column, so the eye can
	// compare down the list. Without the width calculation the bars would step.
	columns := make([]int, 0, len(lines))
	for _, line := range lines {
		columns = append(columns, strings.Index(line, " 0."))
	}
	assert.Len(t, distinctInts(columns), 1, "the probability column is ragged: %v", columns)
}

func TestWriteReportReportsAWriteFailure(t *testing.T) {
	t.Run("while drawing a hazard", func(t *testing.T) {
		err := writeReport(failingWriter{}, sampleResult(), false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "broken pipe")
	})

	t.Run("while drawing a scale", func(t *testing.T) {
		// Only a scale, so the failure lands in the scale loop rather than the
		// hazard loop; both have to return it rather than drop it.
		err := writeReport(failingWriter{}, newResult(map[string]sdk.Answer{
			"s": sdk.ScoreAnswer{Score: 1, Confidence: 1, Legend: []string{"a", "b"}},
		}), false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "broken pipe")
	})

	t.Run("while writing the separator line", func(t *testing.T) {
		// Two hazards then the blank line: the separator is the first write whose
		// error is easy to discard, because its result is natural to ignore.
		err := writeReport(&failAfter{remaining: 2}, sampleResult(), false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "broken pipe")
	})
}

// ---------------------------------------------------------------------------
// Which rendering a caller gets
// ---------------------------------------------------------------------------

func TestWriteResultChoosesTheRenderingForTheCaller(t *testing.T) {
	cases := []struct {
		name     string
		asJSON   bool
		terminal bool
		wantJSON bool
		why      string
	}{
		{
			name: "a terminal, unasked", terminal: true,
			why: "a person reading a terminal wants the ranked view",
		},
		{
			name: "a terminal, asked for JSON", asJSON: true, terminal: true, wantJSON: true,
			why: "--json means JSON even where the human view would be drawn, for someone " +
				"about to paste it elsewhere",
		},
		{
			name: "a pipe, unasked", wantJSON: true,
			why: "something else is reading, and guessing wrong here is what makes a tool " +
				"unusable in a script",
		},
		{
			name: "a pipe, asked for JSON", asJSON: true, wantJSON: true,
			why: "both reasons agree, and neither may cancel the other out",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, writeResult(&buf, sampleResult(), tc.asJSON, tc.terminal, false))

			assert.Equal(t, tc.wantJSON, strings.HasPrefix(buf.String(), "["), tc.why)

			if tc.wantJSON {
				var decoded []map[string]any
				require.NoError(t, json.Unmarshal(buf.Bytes(), &decoded))
			}
		})
	}
}

// TestWriteResultKeepsColourInTheHumanView checks the arguments are not crossed.
// Colour belongs to the ranked view only: an escape sequence inside the JSON
// would corrupt the document for whatever parses it.
func TestWriteResultKeepsColourInTheHumanView(t *testing.T) {
	var colored, plain, asJSON bytes.Buffer

	require.NoError(t, writeResult(&colored, sampleResult(), false, true, true))
	require.NoError(t, writeResult(&plain, sampleResult(), false, true, false))
	require.NoError(t, writeResult(&asJSON, sampleResult(), true, true, true))

	assert.Contains(t, colored.String(), ansiRed)
	assert.NotContains(t, plain.String(), "\x1b", "colour leaked into the plain rendering")
	assert.NotContains(t, asJSON.String(), "\x1b", "colour leaked into the JSON")
	require.True(t, json.Valid(asJSON.Bytes()), "the JSON was corrupted by colour codes")
}

// ---------------------------------------------------------------------------
// Level names
// ---------------------------------------------------------------------------

func TestLevelTextNamesTheNearestLevel(t *testing.T) {
	legend := []string{"none", "mild", "serious", "severe"}

	cases := []struct {
		score float64
		want  string
	}{
		{0, "none"},
		{0.4, "none"},
		{0.5, "mild"}, // exactly between two levels, and it rounds up
		{1.0, "mild"},
		{2.42, "serious"}, // the shape the live service returns: a score between levels
		{2.5, "severe"},
		{3.0, "severe"},
		{3.9, "severe"}, // above the top level, clamped rather than out of range
		{-0.6, "none"},  // below the bottom, clamped
		{-5, "none"},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%.2f", tc.score), func(t *testing.T) {
			assert.Equal(t, tc.want, levelText(sdk.ScoreAnswer{Score: tc.score, Legend: legend}),
				"a score landing outside the rubric must still name a level: an index "+
					"out of range here is a panic in the middle of printing a report")
		})
	}
}

func TestLevelTextOnARubricWithNoDescription(t *testing.T) {
	// A level the host left undescribed decodes as an empty string, and an entirely
	// undescribed rubric leaves the slice nil or empty.
	assert.Equal(t, "", levelText(sdk.ScoreAnswer{Score: 2, Legend: nil}))
	assert.Equal(t, "", levelText(sdk.ScoreAnswer{Score: 2, Legend: []string{}}),
		"levelText must not index into an empty rubric")
}

// TestLevelLabelDropsTheWorkedExamples pins the fix for a report that was four
// wrapped lines tall next to a table of one-line bars.
//
// The criteria are written summary first, so the first sentence is the label. This
// is a property of the question set rather than of the renderer, which is why it is
// asserted here: a criterion reworded to lead with its examples would silently make
// the terminal view unreadable again, and nothing else would notice.
func TestLevelLabelDropsTheWorkedExamples(t *testing.T) {
	cases := []struct {
		level string
		want  string
	}{
		{"Not sexual, or a legitimate non-explicit topic. This covers sex education.", "Not sexual, or a legitimate non-explicit topic."},
		{"Not sexual, or a legitimate non-explicit topic.", "Not sexual, or a legitimate non-explicit topic."},
		{"no punctuation at all", "no punctuation at all"},
		{"", ""},
	}

	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			answer := sdk.ScoreAnswer{Score: 0, Legend: []string{tc.level}}
			assert.Equal(t, tc.want, levelLabel(answer))
		})
	}

	// The real question set must keep the property, or the terminal view regresses
	// the moment someone rewrites a level.
	for name, question := range questions.Moderation() {
		scored, isScore := question.(sdk.ScoreQuestion)
		if !isScore {
			continue
		}

		legend := make([]string, len(scored.Criteria))
		for i, level := range scored.Criteria {
			text, isString := level.(string)
			require.True(t, isString, "%s level %d is not text", name, i)
			legend[i] = text
		}

		for i := range legend {
			answer := sdk.ScoreAnswer{Score: float64(i), Legend: legend}
			label := levelLabel(answer)
			assert.NotEmpty(t, label, "%s level %d has no label at all", name, i)
			assert.LessOrEqual(t, len(label), 100,
				"%s level %d leads with its examples rather than a summary, so the "+
					"terminal view will wrap", name, i)
		}
	}
}

func TestScaleBarFillsOneCellPerLevel(t *testing.T) {
	legend := []string{"a", "b", "c", "d"} // four levels, top is 3

	cases := []struct {
		score float64
		want  string
	}{
		{0, "····"},
		{0.5, "█···"},
		{1, "█···"},
		{2, "███·"},
		{3, "████"},
		{9, "████"},  // above the top, clamped
		{-3, "····"}, // below the bottom, clamped
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%.1f", tc.score), func(t *testing.T) {
			got := scaleBar(sdk.ScoreAnswer{Score: tc.score, Legend: legend})
			assert.Equal(t, tc.want, got, "the top level must fill every cell")
			assert.Len(t, []rune(got), len(legend), "one cell per level, never more")
		})
	}

	t.Run("a rubric too small to be one", func(t *testing.T) {
		// ValidateQuestions rejects fewer than two levels, so this is only reachable
		// through a hand-built Result. It must return rather than divide by zero.
		assert.Equal(t, "", scaleBar(sdk.ScoreAnswer{Score: 1, Legend: nil}))
		assert.Equal(t, "", scaleBar(sdk.ScoreAnswer{Score: 1, Legend: []string{"only"}}))
	})
}

// TestTheScaleLineIsColouredLikeTheHazards is the regression guard on the two
// sections reading as one table. The scale was once printed without going through
// paint at all, so a report could show a red 0.93 above an uncoloured 3.00.
func TestTheScaleLineIsColouredLikeTheHazards(t *testing.T) {
	legend := []string{"none", "mild", "serious", "severe"}

	cases := []struct {
		score float64
		want  string
	}{
		{0, ansiDim},
		{1, ansiYellow},
		{3, ansiRed},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%.1f", tc.score), func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, writeReport(&buf, newResult(map[string]sdk.Answer{
				"s": sdk.ScoreAnswer{Score: tc.score, Confidence: 1, Legend: legend},
			}), true))

			assert.Contains(t, buf.String(), tc.want,
				"the scale section is coloured on the same thresholds as the hazards, "+
					"so that one report does not look like two")
		})
	}
}

// ---------------------------------------------------------------------------
// Colour
// ---------------------------------------------------------------------------

func TestPaintColoursByHowFarUpTheRangeAValueIs(t *testing.T) {
	cases := []struct {
		value float64
		want  string
	}{
		{0.0, ansiDim + "x" + ansiReset},
		{0.29, ansiDim + "x" + ansiReset},
		{0.3, ansiYellow + "x" + ansiReset}, // the threshold itself is included
		{0.69, ansiYellow + "x" + ansiReset},
		{0.7, ansiRed + "x" + ansiReset},
		{1.0, ansiRed + "x" + ansiReset},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%.2f", tc.value), func(t *testing.T) {
			assert.Equal(t, tc.want, paint("x", tc.value, true))
		})
	}
}

func TestPaintLeavesTheLineUntouchedWhenColourIsOff(t *testing.T) {
	// The thresholds above are presentation only, so with colour off there must be
	// no trace of them in the bytes.
	for _, value := range []float64{0, 0.3, 0.7, 1} {
		assert.Equal(t, "x", paint("x", value, false))
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// sampleResult is the fixture the output tests share: two Nouls at different
// strengths and one four-level scale, which is the shape the real question set
// produces.
func sampleResult() *sdk.Result {
	return newResult(map[string]sdk.Answer{
		"alpha": sdk.NoulAnswer{Noul: 0.90},
		"beta":  sdk.NoulAnswer{Noul: 0.40},
		"zzz": sdk.ScoreAnswer{
			Score:         2.50,
			Confidence:    0.80,
			Legend:        []string{"none", "mild", "serious", "severe"},
			Probabilities: []float64{0, 0.1, 0.3, 0.6},
		},
	})
}

// newResult builds a Result from an inline map, so that a test reads as the
// answers it is about rather than as six lines of construction.
func newResult(answers map[string]sdk.Answer) *sdk.Result {
	return &sdk.Result{Model: "jev-test", Provider: "test", ID: "req-1", Answers: answers}
}

func hazardNames(hazards []hazard) []string {
	out := make([]string, len(hazards))
	for i, h := range hazards {
		out[i] = h.name
	}
	return out
}

func sortedKeysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func distinctInts(values []int) []int {
	seen := map[int]bool{}
	out := make([]int, 0, len(values))
	for _, v := range values {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// failingWriter stands in for a closed pipe. Writing to a program that has already
// exited is ordinary, not exotic, so every write path has to return the error
// rather than discard it with `_`.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write |1: broken pipe")
}

// failAfter succeeds a fixed number of times and then fails, so a test can reach a
// write that happens after the ones it has already checked.
type failAfter struct {
	remaining int
}

func (w *failAfter) Write(p []byte) (int, error) {
	if w.remaining <= 0 {
		return 0, errors.New("write |1: broken pipe")
	}
	w.remaining--
	return len(p), nil
}
