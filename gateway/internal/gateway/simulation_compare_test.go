package gateway

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/cipherTing/sael/gateway/internal/policy"
)

func TestPolicyComparisonClassifiesQuestionUnionOnceWithoutSaving(t *testing.T) {
	classifier := &subsetClassifier{}
	s, store, _ := makeServer(t, activePolicy(), &testClassifier{})
	s.Classifier = classifier
	store.jev = JevConfig{BaseURL: "https://jev.example/v1", Model: "jev", APIKey: "test-classifier-key"}
	active := policy.Policy{Enabled: true, Scenes: []policy.Scene{{ID: "active", Name: "active", Match: policy.Any, Action: policy.Allow, Conditions: []policy.Condition{{Question: "self_harm", Threshold: .8}}}}}
	draft := policy.Policy{Enabled: true, Scenes: []policy.Scene{{ID: "draft", Name: "draft", Match: policy.Any, Action: policy.Block, Conditions: []policy.Condition{{Question: "gore", Threshold: 1.5}}}}}
	s.Classifier = classifier
	raw, _ := json.Marshal(map[string]any{"text": "current input", "endpoint": "openai_chat", "policy": draft, "compare_policy": active})
	w := adminRequest(t, s, http.MethodPost, "/admin/policy/test", string(raw))
	if w.Code != http.StatusOK {
		t.Fatalf("comparison: %d %s", w.Code, w.Body.String())
	}
	if !reflect.DeepEqual(classifier.requested, [][]string{{"self_harm", "gore"}}) {
		t.Fatalf("must classify the canonical question union once: %v", classifier.requested)
	}
	var result struct {
		Scores   []policy.Answer `json:"scores"`
		Decision policy.Decision `json:"decision"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Scores) != 2 || result.Decision.SceneID != "draft" {
		t.Fatalf("comparison scores/decision: %s %v", w.Body.String(), err)
	}
	if len(store.events) != 0 || len(store.counts) != 0 || store.policy.Scenes[0].ID != activePolicy().Scenes[0].ID {
		t.Fatal("test changed live policy or recorded production traffic")
	}
}

func TestPolicyComparisonRejectsInvalidOtherPolicyBeforeClassifying(t *testing.T) {
	classifier := &subsetClassifier{}
	s, _, _ := makeServer(t, activePolicy(), &testClassifier{})
	s.Classifier = classifier
	raw, _ := json.Marshal(map[string]any{"text": "current input", "policy": activePolicy(), "compare_policy": policy.Policy{Scenes: []policy.Scene{{ID: "invalid"}}}})
	w := adminRequest(t, s, http.MethodPost, "/admin/policy/test", string(raw))
	if w.Code != http.StatusBadRequest || len(classifier.requested) != 0 {
		t.Fatalf("invalid comparison must not classify: %d %v", w.Code, classifier.requested)
	}
}

func TestPolicyComparisonWithNoApplicableSceneDoesNotClassify(t *testing.T) {
	classifier := &subsetClassifier{}
	s, _, _ := makeServer(t, activePolicy(), &testClassifier{})
	s.Classifier = classifier
	p := policy.Policy{Scenes: []policy.Scene{{ID: "images", Name: "images", Match: policy.Any, Action: policy.Block, Endpoints: []string{"openai_images"}, Conditions: []policy.Condition{{Question: "gore", Threshold: 1.5}}}}}
	s.Classifier = classifier
	raw, _ := json.Marshal(map[string]any{"text": "current input", "endpoint": "openai_chat", "policy": p, "compare_policy": p})
	w := adminRequest(t, s, http.MethodPost, "/admin/policy/test", string(raw))
	if w.Code != http.StatusOK || len(classifier.requested) != 0 {
		t.Fatalf("nonapplicable policies classify: %d %s", w.Code, w.Body.String())
	}
}
