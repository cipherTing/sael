package questions

// moderation_test.go pins the moderation question set.
//
// This package is the project's core asset: everything around it is plumbing,
// and what the model decides depends on the wording here. That makes the wording
// the one thing in the repository that must not change by accident — a dropped
// carve-out clause or a reworded instruction silently moves the false-positive
// rate, and nothing else in the pipeline would notice.
//
// So there are two kinds of test here, doing different jobs:
//
//   - Structural tests assert the properties the set is supposed to have: ten
//     questions, nine Nouls and one Score, every Noul with both sides written,
//     every id distinct and absent from its own instruction. These say what the
//     set means.
//   - A golden file records the exact text. It asserts nothing about quality; it
//     exists so that any change to a word lands in a diff a human reads, instead
//     of arriving as a one-character edit inside a 238-line file.
//
// Everything here is offline and needs no credentials.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cipherTing/sael/systemone"
)

// update rewrites the golden file. Regenerate deliberately, read the diff, and
// say why in the commit message: a golden file that is rewritten without being
// read is worse than no golden file, because it launders the change.
var update = flag.Bool("update", false, "rewrite the golden files under testdata")

// goldenPath is the recorded wording.
const goldenPath = "testdata/moderation.golden.txt"

// expectedQuestions is the set, by name, with the type each must have. Listing
// it here rather than deriving it from Moderation() is the point: a question that
// disappears or changes type should fail this test, not silently redefine it.
var expectedQuestions = map[string]string{
	"cyber_abuse":     "noul",
	"illicit":         "noul",
	"violence":        "noul",
	"sexual":          "score",
	"child_safety":    "noul",
	"hate_harassment": "noul",
	"privacy_pii":     "noul",
	"fraud_deception": "noul",
	"self_harm":       "noul",
	"bypass_attempt":  "noul",
}

// minFalseSideLen is the shortest a Noul's false side may be.
//
// The number is a floor, not a target, and it guards one specific mistake: the
// false side is where every benign reading lives, so replacing it with "It is
// fine." or "No." removes the boundary cases that keep security researchers,
// novelists and clinicians from being flagged. Length is a crude proxy for
// "the carve-outs are still written down", but it is the only one available
// without asserting on prose, and prose assertions break on every reword.
const minFalseSideLen = 100

func TestModerationHasTheExpectedQuestions(t *testing.T) {
	got := Moderation()

	require.Len(t, got, len(expectedQuestions),
		"the set changed size; every added question costs tokens on every request and "+
			"needs a reason recorded in the package doc")

	names := sortedNames(got)
	assert.Equal(t, sortedKeys(expectedQuestions), names,
		"the set is keyed by the name each answer comes back under, so a rename is a "+
			"breaking change for anyone reading the JSON")

	counts := map[string]int{}
	for _, name := range names {
		counts[got[name].QuestionType()]++
		assert.Equal(t, expectedQuestions[name], got[name].QuestionType(),
			"question %q changed type; a Noul and a Score answer are not "+
				"interchangeable, and a value cannot be carried across", name)
	}

	assert.Equal(t, 9, counts["noul"])
	assert.Equal(t, 1, counts["score"],
		"exactly one graded question is deliberate: see the comment above \"sexual\" "+
			"in moderation.go for why severity is not a second score")
	assert.Zero(t, counts["choice"],
		"a Choice answer names a label; nothing in the set needs a named category yet")
}

// TestModerationPassesTheClientsOwnValidation is the load-bearing test in this
// file. The client refuses to send a set the service would reject, so a set that
// fails validation makes every `sael check` fail before it reaches the network.
//
// It catches the failure mode that is easiest to introduce while editing: a Noul
// whose criteria object is half filled. The marshaller turns a nil side into an
// explicit JSON null, and the endpoint answers that with HTTP 400.
func TestModerationPassesTheClientsOwnValidation(t *testing.T) {
	require.NoError(t, systemone.ValidateQuestions(Moderation()),
		"the question set is not sendable; every request would fail before leaving the process")
}

func TestEveryQuestionStatesItsInstruction(t *testing.T) {
	for _, name := range sortedNames(Moderation()) {
		q := Moderation()[name]
		instruction := questionInstruction(q)

		assert.NotEmpty(t, instruction, "question %q has no instruction", name)
		assert.NotEqual(t, strings.TrimSpace(instruction), "",
			"question %q has a whitespace-only instruction", name)
	}
}

// TestInstructionsAreDistinct guards against a copy-paste edit leaving two
// questions asking the same thing.
//
// Questions in one request are evaluated in parallel and in isolation, so a
// duplicated instruction produces two answers that agree by construction and
// spend tokens twice. The duplicate would look like corroboration in the output.
func TestInstructionsAreDistinct(t *testing.T) {
	seen := map[string]string{}

	for _, name := range sortedNames(Moderation()) {
		instruction := questionInstruction(Moderation()[name])
		if first, ok := seen[instruction]; ok {
			t.Errorf("questions %q and %q have the same instruction", first, name)
			continue
		}
		seen[instruction] = name
	}
}

// TestInstructionsStandAlone pins the rule that the id is never sent to the
// model: only the instruction is, so an instruction that refers to its own key
// refers to something the model cannot see.
//
// It is a narrow check and it is meant to be. It catches the phrasing that comes
// naturally while editing, "For cyber_abuse, decide whether ...", which would
// otherwise read as a sensible question and score against a name the model was
// never given.
func TestInstructionsStandAlone(t *testing.T) {
	for _, name := range sortedNames(Moderation()) {
		instruction := strings.ToLower(questionInstruction(Moderation()[name]))

		// Matched on word boundaries rather than as a substring: "sexual" is a
		// substring of "sexually", and the scaled question legitimately asks how
		// sexually explicit the content is. A substring check would fail on correct
		// wording, which is the fastest way to get a test deleted.
		assert.NotRegexp(t, wordBoundary(name), instruction,
			"question %q names itself in its instruction, but the id is never sent "+
				"to the model", name)
		assert.NotRegexp(t, wordBoundary(strings.ReplaceAll(name, "_", " ")), instruction,
			"question %q names itself in prose in its instruction", name)
		assert.NotContains(t, instruction, "question id",
			"question %q refers to an id the model never receives", name)
	}
}

// wordBoundary matches phrase only as a whole word, so that an id is caught when
// it is used as a name and not when it merely appears inside a longer word.
func wordBoundary(phrase string) *regexp.Regexp {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(strings.ToLower(phrase)) + `\b`)
}

func TestQuestionIDsFollowOneConvention(t *testing.T) {
	for _, name := range sortedNames(Moderation()) {
		assert.Regexp(t, `^[a-z][a-z0-9_]*$`, name,
			"ids are lower snake case, so that they read the same in shell, JSON and Go")
		assert.Equal(t, strings.ToLower(name), name, "id %q is not lower case", name)
	}
}

// TestEveryNoulWritesBothSides checks the structural half of "the boundary
// belongs in criteria", which is the rule the package doc calls out as the reason
// a question usually gets the wrong answer.
func TestEveryNoulWritesBothSides(t *testing.T) {
	nouls := 0

	for _, name := range sortedNames(Moderation()) {
		noul, ok := Moderation()[name].(systemone.NoulQuestion)
		if !ok {
			continue
		}
		nouls++

		require.NotNil(t, noul.Criteria,
			"noul %q omits criteria entirely; the benign readings have nowhere to live "+
				"and the model answers the text alone", name)
		assert.NotNil(t, noul.Criteria.True, "noul %q has a null true side", name)
		assert.NotNil(t, noul.Criteria.False, "noul %q has a null false side", name)
	}

	assert.Equal(t, 9, nouls, "the number of Noul questions changed")
}

// TestEveryNoulFalseSideCarriesTheBenignReading is the guard on the single
// biggest source of false positives: a false side that does not say, in so many
// words, which innocent requests must answer no.
//
// The check is a length floor rather than a content match. Asserting on prose
// would break on every reword and train the reader to ignore this test; the floor
// only fires when the carve-outs have actually been removed.
func TestEveryNoulFalseSideCarriesTheBenignReading(t *testing.T) {
	for _, name := range sortedNames(Moderation()) {
		noul, ok := Moderation()[name].(systemone.NoulQuestion)
		if !ok || noul.Criteria == nil {
			continue
		}

		falseSide := entryText(noul.Criteria.False)
		assert.GreaterOrEqual(t, len(falseSide), minFalseSideLen,
			"noul %q has a short false side. It is where the benign readings are "+
				"written down -- security research, fiction, news, a clinician's "+
				"question -- and dropping it is what makes this category fire on "+
				"the callers a false positive harms most", name)
	}
}

// TestNoulCriteriaNeverUsesTheEmptyString pins that an outcome is described or
// the criteria object is absent, never present-but-blank. An empty string is a
// legal entry and passes validation, so nothing else would catch it.
func TestNoulCriteriaNeverUsesTheEmptyString(t *testing.T) {
	for _, name := range sortedNames(Moderation()) {
		noul, ok := Moderation()[name].(systemone.NoulQuestion)
		if !ok || noul.Criteria == nil {
			continue
		}

		assert.NotEmpty(t, entryText(noul.Criteria.True), "noul %q has a blank true side", name)
		assert.NotEmpty(t, entryText(noul.Criteria.False), "noul %q has a blank false side", name)
	}
}

func TestTheScaledQuestionIsAnOrderedRubric(t *testing.T) {
	scored := 0

	for _, name := range sortedNames(Moderation()) {
		question, ok := Moderation()[name].(systemone.ScoreQuestion)
		if !ok {
			continue
		}
		scored++

		// Two is the smallest rubric that means anything and ten is the largest the
		// API accepts; ValidateQuestions enforces both, and this states the intent
		// so that a rubric quietly shrinking to two levels is visible.
		assert.Len(t, question.Criteria, 4,
			"the %s rubric has a documented four levels, each drawn from a published "+
				"taxonomy; see the comment in moderation.go", name)

		for i, level := range question.Criteria {
			assert.NotEmpty(t, entryText(level),
				"level %d of %q is empty, so a score landing there has no text to "+
					"report and the level index still occupies the scale", i, name)
		}

		// A scale is only readable if its levels differ. Two identical levels would
		// make the score ambiguous to interpret and impossible to threshold.
		assert.Len(t, distinct(entriesText(question.Criteria)), len(question.Criteria),
			"the %s rubric repeats a level description, which leaves the score "+
				"between them undefined", name)
	}

	assert.Equal(t, 1, scored, "the number of Score questions changed")
}

// TestTheScaledQuestionStartsAtTheBenignEnd pins the ordering convention the
// package doc relies on: level 0 is the harmless end, so a threshold can be
// expressed as ">= n". Reversed levels would silently invert every threshold a
// caller had already tuned.
func TestTheScaledQuestionStartsAtTheBenignEnd(t *testing.T) {
	question, ok := Moderation()["sexual"].(systemone.ScoreQuestion)
	require.True(t, ok, "\"sexual\" is expected to be the one Score question")

	first := strings.ToLower(entryText(question.Criteria[0]))
	assert.Contains(t, first, "not sexual",
		"level 0 is no longer the benign end of the scale")

	for i, level := range question.Criteria[1:] {
		assert.NotContains(t, strings.ToLower(entryText(level)), "not sexual",
			"level %d claims the benign reading, but level 0 is where it belongs", i+1)
	}
}

// TestModerationIsDeterministic checks that two calls agree. The set is built
// fresh on every call, so a helper that captured a shared slice or map would show
// up here as one call mutating the next.
func TestModerationIsDeterministic(t *testing.T) {
	first, second := Moderation(), Moderation()

	require.Equal(t, sortedNames(first), sortedNames(second))
	for _, name := range sortedNames(first) {
		assert.Equal(t, first[name], second[name], "question %q differs between calls", name)
	}
}

// TestModerationWordingMatchesGolden is the regression guard on the text itself.
//
// It asserts nothing about whether the wording is any good. It asserts that the
// wording cannot change without someone seeing it, which is the only property a
// test can hold over prose that is expected to be tuned.
//
// Run with -update to rewrite the file, then read the diff before committing.
func TestModerationWordingMatchesGolden(t *testing.T) {
	got := renderModeration(Moderation())

	if *update {
		require.NoError(t, os.MkdirAll(filepath.Dir(goldenPath), 0o755))
		require.NoError(t, os.WriteFile(goldenPath, []byte(got), 0o644))
		t.Logf("rewrote %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err,
		"the golden file is missing; regenerate it with "+
			"`go test ./internal/questions -update` and read the diff")

	// Compared line by line rather than as one string. require.Equal on the whole
	// file prints the entire question set twice and buries the one line that
	// changed, which is the only part anyone needs to read. A golden test is only
	// worth having if its failure names the change.
	const regenerate = "regenerate with `go test ./internal/questions -update`, " +
		"then read the diff and explain the change in the commit message: this " +
		"text is what the model is actually asked, so editing it moves the " +
		"false-positive rate"

	wantLines := strings.Split(string(want), "\n")
	gotLines := strings.Split(got, "\n")

	if !assert.Equal(t, len(wantLines), len(gotLines),
		"the recorded wording grew or shrank; "+regenerate) {
		return
	}

	changed := 0
	for i := range wantLines {
		if wantLines[i] == gotLines[i] {
			continue
		}
		changed++
		t.Errorf("line %d changed; %s\n  recorded: %s\n  now:      %s",
			i+1, regenerate, wantLines[i], gotLines[i])
	}

	assert.Zero(t, changed, "the moderation wording changed on %d line(s)", changed)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// renderModeration renders the set as a stable, diffable text form: one field per
// line, in id order. One field per line is the point — a single reworded clause
// then shows up as a single changed line rather than as one enormous changed
// string.
func renderModeration(set systemone.Questions) string {
	var b strings.Builder

	b.WriteString("# The moderation question set, as sent to the model.\n")
	b.WriteString("#\n")
	b.WriteString("# Regenerate with `go test ./internal/questions -update`, then read the diff.\n")
	b.WriteString("# Every line below is text the model is asked. Nothing here is derived\n")
	b.WriteString("# from the implementation except the ordering, which is by id.\n\n")

	for _, name := range sortedNames(set) {
		fmt.Fprintf(&b, "%s\n", name)

		switch question := set[name].(type) {
		case systemone.NoulQuestion:
			fmt.Fprintf(&b, "  type          noul\n")
			fmt.Fprintf(&b, "  instructions  %s\n", entryText(question.Instructions))
			if question.Criteria != nil {
				fmt.Fprintf(&b, "  true          %s\n", entryText(question.Criteria.True))
				fmt.Fprintf(&b, "  false         %s\n", entryText(question.Criteria.False))
			}

		case systemone.ScoreQuestion:
			fmt.Fprintf(&b, "  type          score\n")
			fmt.Fprintf(&b, "  instructions  %s\n", entryText(question.Instructions))
			for i, level := range question.Criteria {
				fmt.Fprintf(&b, "  level %-2d      %s\n", i, entryText(level))
			}

		default:
			fmt.Fprintf(&b, "  type          %s\n", set[name].QuestionType())
		}

		b.WriteString("\n")
	}

	return b.String()
}

// questionInstruction returns a question's instruction text whatever its type.
func questionInstruction(q systemone.Question) string {
	switch question := q.(type) {
	case systemone.NoulQuestion:
		return entryText(question.Instructions)
	case systemone.ScoreQuestion:
		return entryText(question.Instructions)
	case systemone.ChoiceQuestion:
		return entryText(question.Instructions)
	default:
		return ""
	}
}

// entryText renders one criterion. The set only uses plain strings, but Entry is
// an alias for any, so anything else is rendered rather than dropped: a test that
// silently saw "" for a non-string entry would report the opposite of the truth.
func entryText(entry systemone.Entry) string {
	if s, ok := entry.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", entry)
}

func entriesText(entries []systemone.Entry) []string {
	out := make([]string, len(entries))
	for i, entry := range entries {
		out[i] = entryText(entry)
	}
	return out
}

// distinct returns the unique values in s, preserving the first-seen order.
func distinct(s []string) []string {
	seen := make(map[string]bool, len(s))
	out := make([]string, 0, len(s))
	for _, v := range s {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func sortedNames(set systemone.Questions) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
