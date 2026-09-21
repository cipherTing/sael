package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/cipherTing/sael/systemone"
)

// sael is run by two different callers, and they want different things.
//
// A person at a terminal is asking "did anything fire here, and how sure is the
// model", and answers that question by looking. A script is asking for a stable
// shape it can parse. So the default output is written for the first and --json
// is written for the second, and a pipe switches to the second automatically.
//
// Neither output carries a decision. Both report what the model said.

const barWidth = 20

// hazard is one yes/no answer, ready to be ranked.
type hazard struct {
	name  string
	value float64
}

// rankedHazards returns the yes/no answers ordered by how strongly they fired.
//
// A JSON object has no order, so this ordering exists for the human view; the
// JSON deliberately does not pretend to have one.
func rankedHazards(result *systemone.Result) []hazard {
	hazards := make([]hazard, 0, len(result.Answers))

	for name, answer := range result.Answers {
		if a, ok := answer.(systemone.NoulAnswer); ok {
			hazards = append(hazards, hazard{name: name, value: a.Noul})
		}
	}

	sort.Slice(hazards, func(i, j int) bool {
		if hazards[i].value != hazards[j].value {
			return hazards[i].value > hazards[j].value
		}
		return hazards[i].name < hazards[j].name
	})

	return hazards
}

// scaledAnswers returns the answers that sit on an ordered scale, by name.
func scaledAnswers(result *systemone.Result) []string {
	names := make([]string, 0, 2)
	for name, answer := range result.Answers {
		if _, ok := answer.(systemone.ScoreAnswer); ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// writeReport renders the answers for a person reading a terminal.
func writeReport(w io.Writer, result *systemone.Result, color bool) error {
	hazards := rankedHazards(result)
	scaled := scaledAnswers(result)

	if len(hazards) == 0 && len(scaled) == 0 {
		_, err := fmt.Fprintln(w, "the response carried no answers")
		return err
	}

	width := 0
	for _, h := range hazards {
		if len(h.name) > width {
			width = len(h.name)
		}
	}

	for _, h := range hazards {
		bar := strings.Repeat("█", int(h.value*barWidth+0.5))
		line := fmt.Sprintf("  %-*s  %4.2f  %s", width, h.name, h.value, bar)
		_, err := fmt.Fprintln(w, paint(line, h.value, color))
		if err != nil {
			return err
		}
	}

	if len(scaled) > 0 && len(hazards) > 0 {
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}

	for _, name := range scaled {
		answer, _ := result.Score(name)
		// The top of the scale is one less than the number of levels, and it is
		// read from the answer rather than assumed, so a question with a
		// different number of levels still reports correctly.
		top := float64(len(answer.Legend) - 1)
		_, err := fmt.Fprintf(w, "  %-*s  %.2f / %.0f   %s\n",
			width, name, answer.Score, top, levelText(answer))
		if err != nil {
			return err
		}
	}

	return nil
}

// levelText names the level the score landed nearest, so a number like 2.42 is
// readable without counting array positions by hand.
func levelText(answer systemone.ScoreAnswer) string {
	if len(answer.Legend) == 0 {
		return ""
	}
	// A score can fall between levels, so it is rounded to the nearest one rather
	// than truncated: 2.42 is nearer "serious" than "mild".
	index := int(answer.Score + 0.5)
	if index < 0 {
		index = 0
	}
	if index >= len(answer.Legend) {
		index = len(answer.Legend) - 1
	}
	return answer.Legend[index]
}

const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiDim    = "\x1b[2m"
)

// paint colours a probability line by how far up its range it is. The
// thresholds are presentation only; nothing in sael acts on them.
func paint(line string, value float64, color bool) string {
	if !color {
		return line
	}
	switch {
	case value >= 0.7:
		return ansiRed + line + ansiReset
	case value >= 0.3:
		return ansiYellow + line + ansiReset
	default:
		return ansiDim + line + ansiReset
	}
}

// orderedResult is the JSON shape. It is a list rather than an object because a
// list keeps the ranking, and the ranking is most of the value: the first entry
// is the one that fired hardest.
type orderedResult struct {
	Question string   `json:"question"`
	Type     string   `json:"type"`
	Value    float64  `json:"value"`
	Scale    []string `json:"scale,omitempty"`

	// Confidence is present only on scale answers, because the API only reports
	// it there. A yes/no answer carries its own certainty in its value.
	Confidence *float64 `json:"confidence,omitempty"`
}

// writeJSON renders the answers for a script: hazards ranked, then the scales.
func writeJSON(w io.Writer, result *systemone.Result) error {
	out := make([]orderedResult, 0, len(result.Answers))

	for _, h := range rankedHazards(result) {
		out = append(out, orderedResult{
			Question: h.name,
			Type:     "noul",
			Value:    h.value,
		})
	}

	for _, name := range scaledAnswers(result) {
		answer, _ := result.Score(name)
		confidence := answer.Confidence
		out = append(out, orderedResult{
			Question:   name,
			Type:       "score",
			Value:      answer.Score,
			Scale:      answer.Legend,
			Confidence: &confidence,
		})
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// The scale text is non-ASCII when the questions are translated, and escaping
	// it would make the output unreadable for no gain. This is not HTML.
	enc.SetEscapeHTML(false)

	return enc.Encode(out)
}
