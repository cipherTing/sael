package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cipherTing/sael/gateway/internal/policy"
)

func adminRequest(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.AddCookie(login(t, s))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestJevConfigRequiresAllFieldsAndNeverReturnsSecret(t *testing.T) {
	s, _, _ := makeServer(t, activePolicy(), &testClassifier{})
	bad := adminRequest(t, s, http.MethodPut, "/admin/jev", `{"base_url":"https://api.example/v1","model":"jev-test"}`)
	if bad.Code != 400 {
		t.Fatalf("incomplete config status=%d body=%s", bad.Code, bad.Body.String())
	}
	good := adminRequest(t, s, http.MethodPut, "/admin/jev", `{"base_url":"https://api.example/v1","model":"jev-test","api_key":"secret-key"}`)
	if good.Code != 200 || strings.Contains(good.Body.String(), "secret-key") || !strings.Contains(good.Body.String(), `"api_key_set":true`) {
		t.Fatalf("save status=%d body=%s", good.Code, good.Body.String())
	}
	get := adminRequest(t, s, http.MethodGet, "/admin/jev", "")
	if get.Code != 200 || strings.Contains(get.Body.String(), "secret-key") || !strings.Contains(get.Body.String(), "jev-test") {
		t.Fatalf("read status=%d body=%s", get.Code, get.Body.String())
	}
	retained := adminRequest(t, s, http.MethodPut, "/admin/jev", `{"base_url":"https://api.example/v1","model":"jev-next"}`)
	if retained.Code != 200 || strings.Contains(retained.Body.String(), "secret-key") {
		t.Fatalf("retain status=%d body=%s", retained.Code, retained.Body.String())
	}
}

func TestJevTextTestReturnsScoresAndSimulatedDecisionWithoutProductionEvents(t *testing.T) {
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": 0.9})}
	s, store, upstream := makeServer(t, activePolicy(), c)
	store.jev = JevConfig{BaseURL: "https://api.example/v1", Model: "jev-test", APIKey: "secret-key"}
	w := adminRequest(t, s, http.MethodPost, "/admin/jev/test", `{"text":"a user's current text"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"scene_id":"block"`) || !strings.Contains(w.Body.String(), `"cyber_abuse"`) {
		t.Fatalf("test status=%d body=%s", w.Code, w.Body.String())
	}
	if c.calls != 1 || *upstream != 0 || len(store.events) != 0 || len(store.counts) != 0 {
		t.Fatalf("side effects classifier=%d upstream=%d events=%d counts=%d", c.calls, *upstream, len(store.events), len(store.counts))
	}
	if policy.Validate(store.policy) != nil {
		t.Fatal("fixture policy invalid")
	}
}

func TestJevTextTestMarksClassifierAsHealthy(t *testing.T) {

	c := &testClassifier{answers: fullAnswers(nil)}
	s, _, _ := makeServer(t, activePolicy(), c)
	store, _ := s.Store.(*testStore)
	store.jev = JevConfig{BaseURL: "https://api.example/v1", Model: "jev-test", APIKey: "secret-key"}
	if w := adminRequest(t, s, http.MethodPost, "/admin/jev/test", `{"text":"hello"}`); w.Code != 200 {
		t.Fatalf("test status=%d body=%s", w.Code, w.Body.String())
	}
	w := adminRequest(t, s, http.MethodGet, "/admin/runtime", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"classifier":"ok"`) {
		t.Fatalf("runtime status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestJevTextTestRejectsIncompleteClassifierOutputEvenBeforePolicySetup(t *testing.T) {
	s, store, _ := makeServer(t, policy.Policy{Version: 1}, &testClassifier{answers: []policy.Answer{{Question: "cyber_abuse", Type: "noul", Value: 0.9}}})
	store.jev = JevConfig{BaseURL: "https://api.example/v1", Model: "jev-test", APIKey: "secret-key"}
	w := adminRequest(t, s, http.MethodPost, "/admin/jev/test", `{"text":"hello"}`)
	if w.Code != 502 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
