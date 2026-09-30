package policy

import (
	"encoding/json"
	"testing"
)

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
	if _, err := Evaluate(p, []Answer{{Question: "illicit", Type: "noul", Value: 0.9}}); err == nil {
		t.Fatal("missing required scene answer must fail")
	}
}

func TestEvaluateUsesOnlyApplicableSceneQuestions(t *testing.T) {
	p := Policy{Enabled: true, Scenes: []Scene{
		{ID: "a", Name: "chat", Match: All, Action: Block, Endpoints: []string{"openai_chat"}, Conditions: []Condition{{Question: "gore", Threshold: 1.5}, {Question: "self_harm", Threshold: 0.8}}},
		{ID: "b", Name: "other model", Match: Any, Action: Block, Models: []string{"other"}, Conditions: []Condition{{Question: "sexual", Threshold: 1}}},
	}}
	got := RequiredQuestions(p, "openai_chat", "current")
	if len(got) != 2 || got[0] != "self_harm" || got[1] != "gore" {
		t.Fatalf("wrong applicable question union: %v", got)
	}
	decision, err := Evaluate(p, []Answer{{Question: "gore", Type: "score", Value: 1.6}, {Question: "self_harm", Type: "noul", Value: 0.9}}, "openai_chat", "current")
	if err != nil || decision.SceneID != "a" || decision.Action != Block {
		t.Fatalf("partial scores did not decide scene: %+v %v", decision, err)
	}
}

func TestValidateAnswersForRejectsMissingAndUnexpectedSubsetScores(t *testing.T) {
	want := []string{"gore"}
	for _, scores := range [][]Answer{
		nil,
		{{Question: "self_harm", Type: "noul", Value: 0.9}},
		{{Question: "gore", Type: "score", Value: 1.5}, {Question: "sexual", Type: "score", Value: 1}},
		{{Question: "gore", Type: "score", Value: 3.1}},
	} {
		if err := ValidateAnswersFor(scores, want); err == nil {
			t.Fatalf("accepted invalid subset response: %+v", scores)
		}
	}
	if err := ValidateAnswersFor([]Answer{{Question: "gore", Type: "score", Value: 1.5}}, want); err != nil {
		t.Fatal(err)
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

func TestSceneFreezeDefaultsOffAndValidatesItsOwnDuration(t *testing.T) {
	var p Policy
	if err := json.Unmarshal([]byte(`{"enabled":true,"scenes":[{"id":"one","name":"one","match":"any","action":"block","conditions":[{"question":"gore","threshold":1}]}]}`), &p); err != nil {
		t.Fatal(err)
	}
	if err := Validate(p); err != nil {
		t.Fatalf("new scenes must not require a freeze duration: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"session_block_enabled":true,"session_block_ttl_seconds":0}`), &p.Scenes[0]); err != nil {
		t.Fatal(err)
	}
	if err := Validate(p); err == nil {
		t.Fatal("enabled scene freeze accepted a missing duration")
	}
}

func TestBlockMessageTemplateUsesOnlySafeRequestContext(t *testing.T) {
	var p Policy
	if err := json.Unmarshal([]byte(`{"block_message":"Blocked {request_id} on {endpoint} ({model})"}`), &p); err != nil {
		t.Fatal(err)
	}
	if got := p.FormatBlockMessage("req-123", "/v1/messages", "claude-test"); got != "Blocked req-123 on /v1/messages (claude-test)" {
		t.Fatalf("unexpected rendered message: %q", got)
	}
	p.BlockMessage = "The {scene} rule matched"
	if err := Validate(p); err == nil {
		t.Fatal("template exposed an unsupported internal placeholder")
	}
	for _, message := range []string{"Blocked {Scene}", "Blocked {scene-name}", "Blocked {request_id"} {
		p.BlockMessage = message
		if err := Validate(p); err == nil {
			t.Fatalf("accepted malformed placeholder %q", message)
		}
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

func TestImagesGroupAppliesToBothOperationsAndLegacyVariationCannotBecomeAll(t *testing.T) {
	scene := Scene{ID: "images", Name: "images", Endpoints: []string{"openai_images"}, Action: Block, Match: Any, Conditions: []Condition{{Question: "gore", Threshold: 1.5}}}
	if !scene.AppliesTo("openai_images_generations") || !scene.AppliesTo("openai_images_edits") || scene.AppliesTo("openai_chat") || scene.AppliesTo("openai_images_variations") {
		t.Fatal("Images group not scoped correctly")
	}
	p := Policy{Scenes: []Scene{{ID: "legacy", Name: "legacy", Endpoints: []string{"openai_images_variations"}}}}
	UpgradeLegacy(&p)
	if p.Scenes[0].Active() || len(p.Scenes[0].Endpoints) == 0 {
		t.Fatal("removed endpoint became all endpoints")
	}
}
