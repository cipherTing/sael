package policy

import (
	"encoding/json"
	"testing"
)

func dispositionPolicy(t *testing.T) Policy {
	t.Helper()
	var p Policy
	if err := json.Unmarshal([]byte(`{"enabled":true,"scenes":[{"id":"mixed","name":"mixed","review_mode":"blocking","match":"any","action":"block","conditions":[{"question":"cyber_abuse","threshold":0.5,"record_only":true},{"question":"illicit","threshold":0.5,"record_only":true},{"question":"violence","threshold":0.5},{"question":"privacy_pii","threshold":0.5}]}]}`), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAnyDispositionUsesOnlyTheMatchedConditions(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []string
		want Action
	}{
		{"none", nil, Allow}, {"A", []string{"cyber_abuse"}, Allow}, {"B", []string{"illicit"}, Allow}, {"AB", []string{"cyber_abuse", "illicit"}, Allow},
		{"C", []string{"violence"}, Block}, {"D", []string{"privacy_pii"}, Block}, {"AC", []string{"cyber_abuse", "violence"}, Block}, {"AD", []string{"cyber_abuse", "privacy_pii"}, Block},
		{"BC", []string{"illicit", "violence"}, Block}, {"BD", []string{"illicit", "privacy_pii"}, Block}, {"CD", []string{"violence", "privacy_pii"}, Block},
		{"ABC", []string{"cyber_abuse", "illicit", "violence"}, Block}, {"ABD", []string{"cyber_abuse", "illicit", "privacy_pii"}, Block}, {"ACD", []string{"cyber_abuse", "violence", "privacy_pii"}, Block}, {"BCD", []string{"illicit", "violence", "privacy_pii"}, Block}, {"ABCD", []string{"cyber_abuse", "illicit", "violence", "privacy_pii"}, Block},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := map[string]float64{}
			for _, key := range tc.keys {
				values[key] = .9
			}
			d, _, err := EvaluateDetailed(dispositionPolicy(t), answers(values), "openai_chat")
			if err != nil || d.Action != tc.want || len(d.Hits) != len(tc.keys) {
				t.Fatalf("decision=%+v err=%v", d, err)
			}
		})
	}
}

func TestFirstMatchStopsLaterSceneEvaluation(t *testing.T) {
	p := dispositionPolicy(t)
	p.Scenes = append(p.Scenes, Scene{ID: "later", Name: "later", Match: Any, Action: Block, Conditions: []Condition{{Question: "violence", Threshold: .5}}})
	d, trace, err := EvaluateDetailed(p, answers(map[string]float64{"cyber_abuse": .9, "violence": .9}), "openai_chat")
	if err != nil || d.SceneID != "mixed" || len(d.AlsoMatched) != 0 || trace[1].Status != "priority_skipped" || len(trace[1].Conditions) != 0 {
		t.Fatalf("decision=%+v trace=%+v err=%v", d, trace, err)
	}
}

func TestNonBlockingCannotConfigureRejection(t *testing.T) {
	var p Policy
	_ = json.Unmarshal([]byte(`{"scenes":[{"id":"a","name":"a","review_mode":"non_blocking","match":"any","action":"block","conditions":[{"question":"gore","threshold":1}]}]}`), &p)
	if Validate(p) == nil {
		t.Fatal("nonblocking rejection accepted")
	}
}

func TestAllDispositionIgnoresIndividualSwitches(t *testing.T) {
	p := dispositionPolicy(t)
	p.Scenes[0].Match = All
	for _, action := range []Action{Allow, Block} {
		p.Scenes[0].Action = action
		d, _, err := EvaluateDetailed(p, answers(map[string]float64{"cyber_abuse": .9, "illicit": .9, "violence": .9, "privacy_pii": .9}), "openai_chat")
		if err != nil || d.Action != action || len(d.Hits) != 4 {
			t.Fatalf("all disposition: %+v %v", d, err)
		}
		for _, hit := range d.Hits {
			if hit.RecordOnly == nil || *hit.RecordOnly != (action == Allow) {
				t.Fatal("individual switch overrode whole-scene disposition")
			}
		}
		d, _, err = EvaluateDetailed(p, answers(map[string]float64{"cyber_abuse": .9, "illicit": .9, "violence": .9}), "openai_chat")
		if err != nil || d.SceneID != "" || d.Action != Allow {
			t.Fatalf("incomplete all scene matched: %+v %v", d, err)
		}
	}
}

func TestLegacyDispositionUpgradeIsIdempotent(t *testing.T) {
	for _, match := range []Match{Any, All} {
		for _, action := range []Action{Allow, Block} {
			p := Policy{Enabled: true, Scenes: []Scene{{ID: "old", Name: "old", Action: action, Match: match, Conditions: []Condition{{Question: "gore", Threshold: 1}}, SessionBlockEnabled: true, SessionBlockTTLSeconds: 60}}}
			UpgradeLegacy(&p)
			if err := Validate(p); err != nil {
				t.Fatal(err)
			}
			if p.Scenes[0].ReviewMode == "" || p.Scenes[0].Conditions[0].RecordOnly != (action == Allow) || p.Scenes[0].SessionBlockEnabled != (action == Block) {
				t.Fatalf("incorrect migration: %+v", p.Scenes[0])
			}
			before, _ := json.Marshal(p)
			UpgradeLegacy(&p)
			after, _ := json.Marshal(p)
			if string(before) != string(after) {
				t.Fatal("migration reapplied disposition")
			}
		}
	}
}

func TestCachedPredicatesOmitValuesAndPreserveZeroLiveScores(t *testing.T) {
	p := dispositionPolicy(t)
	facts := map[ConditionKey]bool{}
	for _, c := range p.Scenes[0].Conditions {
		facts[c.Key()] = c.Question == "cyber_abuse"
	}
	d, trace, err := EvaluatePredicates(p, facts, "openai_chat", "")
	if err != nil || d.Action != Allow || d.Reason != "condition_record_only" {
		t.Fatalf("cached disposition: %+v %v", d, err)
	}
	raw, _ := json.Marshal(struct {
		Decision Decision
		Trace    []SceneTrace
	}{d, trace})
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	for _, condition := range decoded["Trace"].([]any)[0].(map[string]any)["conditions"].([]any) {
		if _, exists := condition.(map[string]any)["value"]; exists {
			t.Fatal("cached comparison fabricated score")
		}
	}
	_, live, err := EvaluateDetailed(p, answers(nil), "openai_chat")
	if err != nil || live[0].Conditions[0].Value == nil || *live[0].Conditions[0].Value != 0 {
		t.Fatal("zero live score became unavailable")
	}
}

func TestHistoricalHitDoesNotInventConditionDisposition(t *testing.T) {
	var hit Hit
	if err := json.Unmarshal([]byte(`{"question":"gore","value":1.9,"threshold":1.5}`), &hit); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(hit)
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	if _, exists := fields["record_only"]; exists {
		t.Fatal("historical hit acquired a made-up rejection disposition")
	}
}
