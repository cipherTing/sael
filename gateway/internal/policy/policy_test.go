package policy

import "testing"

func readyPolicy() Policy {
	thresholds := map[string]float64{}
	for _, q := range Questions {
		thresholds[q.Key] = q.Max / 2
	}
	preview, days := 0, 30
	return Policy{Enabled: true, Version: 1, Thresholds: thresholds, UnmatchedAction: Allow, PreviewChars: &preview, RetentionDays: &days}
}

func answers(values map[string]float64) []Answer {
	out := make([]Answer, 0, len(Questions))
	for _, q := range Questions {
		out = append(out, Answer{Question: q.Key, Type: q.Type, Value: values[q.Key]})
	}
	return out
}

func TestScoreMustExceedThreshold(t *testing.T) {
	p := readyPolicy()
	p.Thresholds["cyber_abuse"] = 0.8
	got, err := Evaluate(p, answers(map[string]float64{"cyber_abuse": 0.8}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Hits) != 0 || got.Action != Allow {
		t.Fatalf("at threshold: %+v", got)
	}
	got, err = Evaluate(p, answers(map[string]float64{"cyber_abuse": 0.81}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Hits) != 1 || got.Hits[0].Question != "cyber_abuse" || got.Action != Allow {
		t.Fatalf("over threshold: %+v", got)
	}
}

func TestFirstMatchingSceneWins(t *testing.T) {
	p := readyPolicy()
	p.Scenes = []Scene{
		{ID: "exception", Name: "both allowed", Questions: []string{"cyber_abuse", "illicit"}, Match: All, Action: Allow},
		{ID: "ordinary", Name: "either blocked", Questions: []string{"cyber_abuse", "illicit"}, Match: Any, Action: Block},
	}
	for _, tt := range []struct {
		values map[string]float64
		scene  string
		action Action
	}{
		{map[string]float64{"cyber_abuse": 0.9}, "ordinary", Block},
		{map[string]float64{"cyber_abuse": 0.9, "illicit": 0.9}, "exception", Allow},
	} {
		got, err := Evaluate(p, answers(tt.values))
		if err != nil {
			t.Fatal(err)
		}
		if got.SceneID != tt.scene || got.Action != tt.action {
			t.Fatalf("got %+v, want %s/%s", got, tt.scene, tt.action)
		}
	}
	p.Scenes[0], p.Scenes[1] = p.Scenes[1], p.Scenes[0]
	got, err := Evaluate(p, answers(map[string]float64{"cyber_abuse": 0.9, "illicit": 0.9}))
	if err != nil {
		t.Fatal(err)
	}
	if got.SceneID != "ordinary" || got.Action != Block {
		t.Fatalf("reordered: %+v", got)
	}
}

func TestUnmatchedHitUsesConfiguredAction(t *testing.T) {
	p := readyPolicy()
	p.UnmatchedAction = Block
	p.Scenes = []Scene{{ID: "other", Name: "other", Questions: []string{"illicit"}, Match: Any, Action: Allow}}
	got, err := Evaluate(p, answers(map[string]float64{"cyber_abuse": 0.9}))
	if err != nil {
		t.Fatal(err)
	}
	if got.Action != Block || got.SceneID != "" || len(got.Hits) != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestIncompleteAnswersAreUnavailable(t *testing.T) {
	_, err := Evaluate(readyPolicy(), []Answer{{Question: "cyber_abuse", Type: "noul", Value: 0.9}})
	if err == nil {
		t.Fatal("missing answers must fail")
	}
}

func TestEnabledPolicyRequiresConfiguredThresholds(t *testing.T) {
	p := Policy{Enabled: true, Thresholds: map[string]float64{}, UnmatchedAction: Allow}
	if err := Validate(p); err == nil {
		t.Fatal("empty policy must not enable review")
	}
	p = readyPolicy()
	p.Thresholds["sexual"] = 3.1
	if err := Validate(p); err == nil {
		t.Fatal("score threshold above scale must fail")
	}
}
