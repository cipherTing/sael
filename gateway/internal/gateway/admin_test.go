package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAnalyticsRejectsInvalidGranularityAndTimezone(t *testing.T) {
	for _, query := range []string{
		"minutes=1440&granularity=42m",
		"minutes=1440&granularity=auto",
		"minutes=1440&timezone=Unknown/Invalid",
	} {
		q, _ := url.ParseQuery(query)
		if _, err := analyticsFilter(q); err == nil {
			t.Errorf("accepted invalid analytics query: %s", query)
		}
	}
}

func TestAnalyticsKeepsTheSelectedFineGranularityForLongerRanges(t *testing.T) {
	q, _ := url.ParseQuery("minutes=4320&granularity=1m&timezone=Asia/Shanghai")
	f, err := analyticsFilter(q)
	if err != nil {
		t.Fatal(err)
	}
	if f.StepSeconds() != 60 {
		t.Fatalf("selected minute granularity changed to %d", f.StepSeconds())
	}
}

func login(t *testing.T, s *Server) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(w, authorizedRequest("POST", "/admin/login", strings.NewReader(`{"password":"secret"}`)))
	if w.Code != 200 {
		t.Fatalf("login status %d: %s", w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "sael_session" && c.HttpOnly {
			return c
		}
	}
	t.Fatal("missing HttpOnly session cookie")
	return nil
}

func TestRuntimeShowsClassifierFailure(t *testing.T) {
	s, _, _ := makeServer(t, activePolicy(), &testClassifier{err: errors.New("unavailable")})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"text"}]}`)))
	cookie := login(t, s)
	r := authorizedRequest("GET", "/admin/runtime", http.NoBody)
	r.AddCookie(cookie)
	runtime := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(runtime, r)
	if runtime.Code != 200 || !strings.Contains(runtime.Body.String(), `"classifier":"error"`) || !strings.Contains(runtime.Body.String(), `"last_error_kind":"classifier_unavailable"`) {
		t.Fatalf("runtime: %d %s", runtime.Code, runtime.Body.String())
	}
}

func TestAdminShowsSavedUpstreamOriginWithoutCredentials(t *testing.T) {
	s, _, _ := makeServer(t, activePolicy(), &testClassifier{})
	w := adminRequest(t, s, http.MethodGet, "/admin/upstream", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"base_url":"http://`) {
		t.Fatalf("upstream status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAdminAPIRequiresSession(t *testing.T) {
	s, _, _ := makeServer(t, activePolicy(), &testClassifier{})
	for _, path := range []string{"/admin/policy", "/admin/upstream", "/admin/overview", "/admin/events"} {
		w := httptest.NewRecorder()
		s.AdminHandler().ServeHTTP(w, authorizedRequest("GET", path, http.NoBody))
		if w.Code != 401 {
			t.Fatalf("%s status=%d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(w, authorizedRequest("POST", "/admin/login", strings.NewReader(`{"password":"wrong"}`)))
	if w.Code != 401 {
		t.Fatalf("wrong password status=%d", w.Code)
	}
}

func TestAdminOverviewAcceptsExplicitDateRange(t *testing.T) {
	s, store, _ := makeServer(t, activePolicy(), &testClassifier{})
	w := adminRequest(t, s, http.MethodGet, "/admin/overview?start=2026-09-23T00:00:00%2B08:00&end=2026-09-24T00:00:00%2B08:00", "")
	start, _ := time.Parse(time.RFC3339, "2026-09-23T00:00:00+08:00")
	end, _ := time.Parse(time.RFC3339, "2026-09-24T00:00:00+08:00")
	if w.Code != 200 || !store.overviewSince.Equal(start) || !store.overviewUntil.Equal(end) {
		t.Fatalf("status=%d since=%s until=%s", w.Code, store.overviewSince, store.overviewUntil)
	}
}

func TestAdminPolicySaveRejectsStaleVersion(t *testing.T) {
	s, store, _ := makeServer(t, activePolicy(), &testClassifier{})
	cookie := login(t, s)
	read := httptest.NewRecorder()
	get := authorizedRequest("GET", "/admin/policy", http.NoBody)
	get.AddCookie(cookie)
	s.AdminHandler().ServeHTTP(read, get)
	if read.Code != 200 || !strings.Contains(read.Body.String(), `"version":2`) {
		t.Fatalf("get policy: %d %s", read.Code, read.Body.String())
	}
	payload := `{"enabled":false,"version":2,"thresholds":{},"scenes":[],"unmatched_action":"allow"}`
	put := authorizedRequest("PUT", "/admin/policy", strings.NewReader(payload))
	put.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(w, put)
	if w.Code != 200 || store.policy.Version != 3 || store.policy.Enabled {
		t.Fatalf("save: %d %+v", w.Code, store.policy)
	}
	stale := authorizedRequest("PUT", "/admin/policy", strings.NewReader(payload))
	stale.AddCookie(cookie)
	w2 := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(w2, stale)
	if w2.Code != 409 || store.policy.Version != 3 {
		t.Fatalf("stale save: %d %+v", w2.Code, store.policy)
	}
}

func TestCannotEnableReviewUntilJevConnectionIsSaved(t *testing.T) {
	p := activePolicy()
	p.Enabled = false
	s, store, _ := makeServer(t, p, &testClassifier{})
	input, err := json.Marshal(activePolicy())
	if err != nil {
		t.Fatal(err)
	}
	w := adminRequest(t, s, http.MethodPut, "/admin/policy", string(input))
	if w.Code != 400 || store.policy.Enabled {
		t.Fatalf("unconfigured status=%d body=%s", w.Code, w.Body.String())
	}
	store.jev = JevConfig{BaseURL: "https://api.example/v1", Model: "jev-test", APIKey: "secret"}
	w = adminRequest(t, s, http.MethodPut, "/admin/policy", string(input))
	if w.Code != 200 || !store.policy.Enabled {
		t.Fatalf("configured status=%d body=%s", w.Code, w.Body.String())
	}
}
