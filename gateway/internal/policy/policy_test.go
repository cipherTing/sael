package policy

import "testing"

func answers(values map[string]float64) []Answer {
	out := make([]Answer, 0, len(Questions))
	for _, q := range Questions {
		out = append(out, Answer{Question: q.Key, Type: q.Type, Value: values[q.Key]})
	}
	return out
}

func TestSceneUsesItsOwnThresholds(t *testing.T) {
	p := Policy{Enabled: true, Scenes: []Scene{
		{ID: "combined", Name: "血腥且自伤", Match: All, Action: Block, Conditions: []Condition{{Question: "gore", Threshold: 1.5}, {Question: "self_harm", Threshold: 0.8}}},
		{ID: "gore", Name: "血腥记录", Match: Any, Action: Allow, Conditions: []Condition{{Question: "gore", Threshold: 1.2}}},
	}}
	got, err := Evaluate(p, answers(map[string]float64{"gore": 1.6, "self_harm": 0.81}))
	if err != nil {
		t.Fatal(err)
	}
	if got.SceneID != "combined" || got.Action != Block || len(got.Hits) != 2 || got.Hits[0].Threshold != 1.5 || got.Hits[1].Threshold != 0.8 {
		t.Fatalf("combined: %+v", got)
	}
	got, err = Evaluate(p, answers(map[string]float64{"gore": 1.3, "self_harm": 0.81}))
	if err != nil {
		t.Fatal(err)
	}
	if got.SceneID != "gore" || got.Action != Allow || len(got.Hits) != 1 || got.Hits[0].Threshold != 1.2 {
		t.Fatalf("fallback: %+v", got)
	}
}

func TestScoreMustStrictlyExceedSceneThreshold(t *testing.T) {
	p := Policy{Enabled: true, Scenes: []Scene{{ID: "a", Name: "攻击", Match: Any, Action: Block, Conditions: []Condition{{Question: "cyber_abuse", Threshold: 0.8}}}}}
	got, err := Evaluate(p, answers(map[string]float64{"cyber_abuse": 0.8}))
	if err != nil {
		t.Fatal(err)
	}
	if got.Action != Allow || len(got.Hits) != 0 {
		t.Fatalf("equal threshold: %+v", got)
	}
}

func TestUnmatchedScoreDoesNotCreateAHit(t *testing.T) {
	p := Policy{Enabled: true, Scenes: []Scene{{ID: "other", Name: "其他", Match: Any, Action: Block, Conditions: []Condition{{Question: "illicit", Threshold: 0.8}}}}}
	got, err := Evaluate(p, answers(map[string]float64{"cyber_abuse": 0.9}))
	if err != nil {
		t.Fatal(err)
	}
	if got.Action != Allow || got.SceneID != "" || len(got.Hits) != 0 {
		t.Fatalf("unmatched: %+v", got)
	}
}

func TestIncompleteAnswersAreUnavailable(t *testing.T) {
	p := Policy{Enabled: true, Scenes: []Scene{{ID: "a", Name: "攻击", Match: Any, Action: Block, Conditions: []Condition{{Question: "cyber_abuse", Threshold: 0.8}}}}}
	if _, err := Evaluate(p, []Answer{{Question: "cyber_abuse", Type: "noul", Value: 0.9}}); err == nil {
		t.Fatal("missing answers must fail")
	}
}

func TestEnabledPolicyRequiresValidSceneConditions(t *testing.T) {
	if err := Validate(Policy{Enabled: true}); err == nil {
		t.Fatal("enabled policy without scenes must fail")
	}
	p := Policy{Enabled: true, Scenes: []Scene{{ID: "a", Name: "血腥", Match: Any, Action: Block, Conditions: []Condition{{Question: "gore", Threshold: 3.1}}}}}
	if err := Validate(p); err == nil {
		t.Fatal("score threshold above its scale must fail")
	}
	p.Scenes[0].Conditions = []Condition{{Question: "gore", Threshold: 1.5}, {Question: "gore", Threshold: 2}}
	if err := Validate(p); err == nil {
		t.Fatal("duplicate question in a scene must fail")
	}
}

func TestLegacyPolicyCopiesGlobalThresholdsIntoScenes(t *testing.T) {
	p := Policy{Enabled: true, Thresholds: map[string]float64{"gore": 1.5, "self_harm": 0.8}, UnmatchedAction: Block,
		Scenes: []Scene{{ID: "old", Name: "旧场景", Questions: []string{"gore", "self_harm"}, Match: All, Action: Block}}}
	UpgradeLegacy(&p)
	if len(p.Scenes[0].Conditions) != 2 || p.Scenes[0].Conditions[0].Threshold != 1.5 || p.Scenes[0].Conditions[1].Threshold != 0.8 {
		t.Fatalf("legacy conditions: %+v", p.Scenes[0].Conditions)
	}
	if p.Thresholds != nil || p.UnmatchedAction != "" || p.Scenes[0].Questions != nil {
		t.Fatalf("legacy fields remained: %+v", p)
	}
}

func TestLegacyPolicyWithOnlyUnmatchedActionStopsReview(t *testing.T) {
	p := Policy{Enabled: true, Thresholds: map[string]float64{"gore": 1.5}, UnmatchedAction: Block}
	UpgradeLegacy(&p)
	if p.Enabled {
		t.Fatal("a legacy policy without scenes cannot review after unmatched handling is removed")
	}
}
