package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cipherTing/sael/gateway/internal/policy"
)

type reviewAPIStore struct {
	*testStore
	keys          map[string]ReviewAPIKey
	stats         []ReviewAPIStat
	created       []ReviewAPIKey
	nextKeySecret string
	authErr       error
	filter        EventFilter
}

func (s *reviewAPIStore) AuthenticateReviewAPIKey(_ context.Context, secret string) (ReviewAPIKey, error) {
	if s.authErr != nil {
		return ReviewAPIKey{}, s.authErr
	}
	for _, key := range s.keys {
		if key.Secret == secret && key.RevokedAt == nil {
			return key, nil
		}
	}
	return ReviewAPIKey{}, ErrInvalidReviewAPIKey
}

func (s *reviewAPIStore) RecordReviewAPIStat(_ context.Context, stat ReviewAPIStat) error {
	s.stats = append(s.stats, stat)
	return nil
}

func (s *reviewAPIStore) CreateReviewAPIKey(_ context.Context, name, note string) (ReviewAPIKey, error) {
	key := ReviewAPIKey{ID: "created", Name: name, Note: note, Secret: s.nextKeySecret, Masked: "sk-sael-review-1234******"}
	s.created = append(s.created, key)
	if s.keys == nil {
		s.keys = map[string]ReviewAPIKey{}
	}
	s.keys[key.ID] = key
	return key, nil
}
func (s *reviewAPIStore) ListReviewAPIKeys(context.Context) ([]ReviewAPIKey, error) {
	items := make([]ReviewAPIKey, 0, len(s.keys))
	for _, key := range s.keys {
		items = append(items, key)
	}
	return items, nil
}
func (s *reviewAPIStore) RevokeReviewAPIKey(_ context.Context, id string) error {
	key := s.keys[id]
	now := time.Now()
	key.RevokedAt = &now
	s.keys[id] = key
	return nil
}
func (s *reviewAPIStore) ReviewAPIOverview(context.Context, time.Time, time.Time) (ReviewAPIOverview, error) {
	return ReviewAPIOverview{}, nil
}

func (s *reviewAPIStore) Events(_ context.Context, f EventFilter) ([]Event, error) {
	s.filter = f
	return s.events, nil
}

func TestReviewAPIIsUnavailableUntilEnabled(t *testing.T) {
	store := &reviewAPIStore{testStore: &testStore{policy: activePolicy()}, keys: map[string]ReviewAPIKey{"key": {ID: "key", Secret: "secret"}}}
	s, _, _ := makeServer(t, store.policy, &testClassifier{answers: fullAnswers(nil)})
	s.Store = store
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/moderations", strings.NewReader(`{"input":"hello"}`)))
	if w.Code != http.StatusNotFound {
		t.Fatalf("disabled review API status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestReviewAPIReturnsSaelDecisionAndRecordsOnlyHits(t *testing.T) {
	p := activePolicy()
	p.ReviewAPIEnabled = true
	p.Scenes = []policy.Scene{{ID: "danger", Name: "危险内容", Conditions: []policy.Condition{{Question: "cyber_abuse", Threshold: .5}}, Match: policy.Any, Action: policy.Block}}
	store := &reviewAPIStore{testStore: &testStore{policy: p, jev: JevConfig{BaseURL: "https://jev.example/v1", Model: "internal", APIKey: "secret", TimeoutMS: 1000}}, keys: map[string]ReviewAPIKey{"key": {ID: "key", Secret: "secret"}}}
	classifier := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": .9})}
	s, _, _ := makeServer(t, p, classifier)
	s.Store = store
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/moderations", strings.NewReader(`{"input":"dangerous text"}`))
	r.Header.Set("Authorization", "Bearer secret")
	s.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("review API status=%d body=%s", w.Code, w.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["object"] != "sael.moderation" || response["action"] != "block" || response["flagged"] != true {
		t.Fatalf("unexpected Sael response: %s", w.Body.String())
	}
	if _, ok := response["model"]; ok {
		t.Fatal("classifier model leaked into review API response")
	}
	if _, ok := response["classifier_ms"]; ok {
		t.Fatal("classifier timing leaked into review API response")
	}
	if len(store.events) != 1 || store.events[0].RequestSource != "review_api" {
		t.Fatalf("hit was not recorded as review_api: %+v", store.events)
	}
	if len(store.stats) != 1 || store.stats[0].RequestSource != "review_api" {
		t.Fatalf("review API stats missing: %+v", store.stats)
	}

	classifier.answers = fullAnswers(nil)
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/v1/moderations", strings.NewReader(`{"input":"safe text"}`))
	r.Header.Set("Authorization", "Bearer secret")
	s.ServeHTTP(w, r)
	if w.Code != http.StatusOK || len(store.events) != 1 {
		t.Fatalf("clean review should return and avoid event record: status=%d events=%d", w.Code, len(store.events))
	}
}

func TestReviewAPIRejectsInvalidKey(t *testing.T) {
	p := activePolicy()
	p.ReviewAPIEnabled = true
	store := &reviewAPIStore{testStore: &testStore{policy: p}, keys: map[string]ReviewAPIKey{}}
	s, _, _ := makeServer(t, p, &testClassifier{})
	s.Store = store
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/moderations", strings.NewReader(`{"input":"hello"}`))
	r.Header.Set("Authorization", "Bearer invalid")
	s.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("invalid review key status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestReviewAPIStatsAreIndependentFromGatewayCounts(t *testing.T) {
	p := activePolicy()
	p.ReviewAPIEnabled = true
	store := &reviewAPIStore{testStore: &testStore{policy: p, jev: JevConfig{BaseURL: "https://jev.example/v1", Model: "internal", APIKey: "secret", TimeoutMS: 1000}}, keys: map[string]ReviewAPIKey{"key": {ID: "key", Secret: "secret"}}}
	s, _, _ := makeServer(t, p, &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": .9})})
	s.Store = store
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/moderations", strings.NewReader(`{"input":"danger"}`))
	r.Header.Set("Authorization", "Bearer secret")
	s.ServeHTTP(w, r)
	if len(store.counts) != 0 {
		t.Fatalf("review API polluted gateway counters: %+v", store.counts)
	}
	if len(store.stats) != 1 {
		t.Fatal("review API stat was not recorded independently")
	}
}

func TestReviewAPIAllowsOverCharacterLimitWithoutCallingClassifier(t *testing.T) {
	s, st, classifier, _ := reviewAPIFixture(t)
	st.jev.MaxInputChars = 5
	w := sendReviewAPI(s, `{"input":"你好世界啊!"}`)
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || response["flagged"] != false || response["action"] != "allow" || classifier.calls != 0 {
		t.Fatalf("over-limit HTTP review was not allowed without Jev: status=%d calls=%d body=%s", w.Code, classifier.calls, w.Body.String())
	}
	if len(st.events) != 0 || len(st.stats) != 1 || st.stats[0].Outcome != "allowed" || len(st.counts) != 0 {
		t.Fatalf("over-limit request accounting is wrong: events=%+v stats=%+v counts=%+v", st.events, st.stats, st.counts)
	}
}

func reviewAPIFixture(t *testing.T) (*Server, *reviewAPIStore, *testClassifier, *int) {
	t.Helper()
	p := activePolicy()
	p.ReviewAPIEnabled = true
	c := &testClassifier{answers: fullAnswers(nil)}
	s, base, forwarded := makeServer(t, p, c)
	base.jev = JevConfig{BaseURL: "https://jev.example/v1", Model: "jev", APIKey: "secret", TimeoutMS: 1000}
	st := &reviewAPIStore{testStore: base, keys: map[string]ReviewAPIKey{"key": {ID: "key", Secret: "secret"}}}
	s.Store = st
	return s, st, c, forwarded
}

func sendReviewAPI(s *Server, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/v1/moderations", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestReviewAPIValidatesJSONAndCountsAllEnabledCalls(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		limit      int64
		status     int
	}{
		{"missing", `{}`, 1024, 400},
		{"array", `{"input":["text"]}`, 1024, 400},
		{"trailing", `{"input":"text"} {}`, 1024, 400},
		{"unknown", `{"input":"text","unexpected":true}`, 1024, 400},
		{"large", `{"input":"long text"}`, 10, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, st, _, forwarded := reviewAPIFixture(t)
			s.MaxBodyBytes = tc.limit
			w := sendReviewAPI(s, tc.body)
			if w.Code != tc.status || !json.Valid(w.Body.Bytes()) {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if len(st.stats) != 1 || st.stats[0].Outcome != "error" || len(st.events) != 0 || len(st.counts) != 0 || *forwarded != 0 {
				t.Fatalf("incorrect accounting: stats=%+v", st.stats)
			}
		})
	}
	s, st, _, _ := reviewAPIFixture(t)
	st.policy.ReviewAPIEnabled = false
	s.MaxBodyBytes = 1
	w := sendReviewAPI(s, `{"input":"text"}`)
	if w.Code != 404 || !json.Valid(w.Body.Bytes()) || len(st.stats) != 0 {
		t.Fatalf("disabled status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestReviewAPICleanResponseHasScoresAndOptionalContext(t *testing.T) {
	s, st, _, forwarded := reviewAPIFixture(t)
	st.policy.Enabled = false
	st.policy.Scenes[0].Endpoints = []string{"anthropic"}
	st.policy.Scenes[0].Models = []string{"claude"}
	w := sendReviewAPI(s, `{"input":"safe"}`)
	var response struct {
		Scores []map[string]any `json:"scores"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	if w.Code != 200 || len(response.Scores) == 0 || response.Scores[0]["type"] != nil || *forwarded != 0 {
		t.Fatalf("clean response=%s", w.Body.String())
	}
	if w = sendReviewAPI(s, `{"input":"safe","endpoint":"openai_responses","model":"other"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("model should be rejected, got %d: %s", w.Code, w.Body.String())
	}
}

func TestReviewAPIRejectsModelField(t *testing.T) {
	s, _, _, _ := reviewAPIFixture(t)
	w := sendReviewAPI(s, `{"input":"safe","model":"gpt-4.1"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_input") {
		t.Fatalf("model field was accepted: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestReviewAPIAuthOutageAndFailureNeverBecomeAuditEvents(t *testing.T) {
	s, st, c, _ := reviewAPIFixture(t)
	st.authErr = errors.New("database unavailable")
	if w := sendReviewAPI(s, `{"input":"text"}`); w.Code != 503 {
		t.Fatalf("database outage status=%d", w.Code)
	}
	st.authErr = ErrInvalidReviewAPIKey
	if w := sendReviewAPI(s, `{"input":"text"}`); w.Code != 401 {
		t.Fatalf("invalid key status=%d", w.Code)
	}
	st.authErr = nil
	c.err = errors.New("classifier unavailable")
	if w := sendReviewAPI(s, `{"input":"text"}`); w.Code != 503 {
		t.Fatalf("classifier outage status=%d", w.Code)
	}
	if len(st.events) != 0 || len(st.stats) != 3 {
		t.Fatalf("failure polluted records or missed counts: %+v", st.stats)
	}
}

func TestReviewAPICacheReusesVerdictWithoutFreezingSession(t *testing.T) {
	s, st, c, forwarded := reviewAPIFixture(t)
	c.answers = fullAnswers(map[string]float64{"cyber_abuse": .9})
	st.policy.Scenes[0].SessionBlockEnabled = true
	st.policy.Scenes[0].SessionBlockTTLSeconds = 60
	s.ReviewCache = &memoryReviewCache{values: map[string]bool{}}
	for range 2 {
		if w := sendReviewAPI(s, `{"input":"danger"}`); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	if c.calls != 1 || len(st.events) != 2 || st.events[1].ReviewSource != "cache" || !st.stats[1].CacheHit || len(st.blocked) != 0 || *forwarded != 0 {
		t.Fatalf("cache behavior: calls=%d stats=%+v", c.calls, st.stats)
	}
}

func TestReviewAPICachedRecordOnlyUsesCurrentConditionsAndOneScene(t *testing.T) {
	s, st, c, forwarded := reviewAPIFixture(t)
	var p policy.Policy
	if err := json.Unmarshal([]byte(`{"scenes":[{"id":"first","name":"first","review_mode":"blocking","match":"any","action":"block","conditions":[{"question":"cyber_abuse","threshold":0.5,"record_only":true},{"question":"illicit","threshold":0.5}]},{"id":"later","name":"later","review_mode":"blocking","match":"any","action":"block","conditions":[{"question":"cyber_abuse","threshold":0.5}]}]}`), &p); err != nil {
		t.Fatal(err)
	}
	st.policy.Scenes = p.Scenes
	c.answers = fullAnswers(map[string]float64{"cyber_abuse": .9})
	s.ReviewCache = &memoryReviewCache{values: map[string]bool{}}
	for i := 0; i < 3; i++ {
		if i == 2 {
			st.policy.Scenes[0].Conditions[0].RecordOnly = false
		}
		w := sendReviewAPI(s, `{"input":"mixed"}`)
		var response reviewAPIResponse
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		want := policy.Allow
		if i == 2 {
			want = policy.Block
		}
		if w.Code != 200 || !response.Flagged || response.Action != want || len(response.Scenes) != 1 || response.Scenes[0].ID != "first" || len(response.Scenes[0].Hits) != 1 {
			t.Fatalf("response=%s", w.Body.String())
		}
		if i > 0 && (len(response.Scores) != 0 || response.Scenes[0].Hits[0].Value != nil) {
			t.Fatal("cached API response fabricated score")
		}
	}
	if c.calls != 1 || *forwarded != 0 || len(st.blocked) != 0 || len(st.events) != 3 || !st.stats[2].CacheHit {
		t.Fatalf("shared cached review: calls=%d forwarded=%d events=%v", c.calls, *forwarded, st.events)
	}
}

func TestReviewAPIAdminKeyLifecycle(t *testing.T) {
	s, st, _, _ := reviewAPIFixture(t)
	st.nextKeySecret = "created-secret"
	w := adminRequest(t, s, "POST", "/admin/review-api/keys", `{"name":"test","note":"note"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "created-secret") {
		t.Fatalf("create=%d %s", w.Code, w.Body.String())
	}
	w = adminRequest(t, s, "GET", "/admin/review-api/keys", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "created-secret") || strings.Contains(w.Body.String(), `"secret"`) {
		t.Fatalf("list=%d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/admin/review-api/overview", "/admin/review-api/analytics"} {
		if w = adminRequest(t, s, "GET", path, ""); w.Code != 200 {
			t.Fatalf("%s=%d %s", path, w.Code, w.Body.String())
		}
	}
	if w = adminRequest(t, s, "DELETE", "/admin/review-api/keys/created", ""); w.Code != 200 {
		t.Fatalf("revoke=%d", w.Code)
	}
	if _, err := st.AuthenticateReviewAPIKey(context.Background(), "created-secret"); !errors.Is(err, ErrInvalidReviewAPIKey) {
		t.Fatal("revoked key stayed valid")
	}
	if w = adminRequest(t, s, "PATCH", "/admin/policy", `{"review_api_enabled":false}`); w.Code != 200 || st.policy.ReviewAPIEnabled || !st.policy.Enabled {
		t.Fatalf("toggle=%d %s", w.Code, w.Body.String())
	}
	if w = adminRequest(t, s, "GET", "/admin/events?request_source=review_api", ""); w.Code != 200 || st.filter.RequestSource != "review_api" {
		t.Fatalf("source filter=%+v", st.filter)
	}
}

func TestReviewAPIMethodNeverForwardsUpstream(t *testing.T) {
	s, _, _, forwarded := reviewAPIFixture(t)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/v1/moderations", http.NoBody))
	if w.Code != 405 || *forwarded != 0 || !json.Valid(w.Body.Bytes()) {
		t.Fatalf("method=%d forwarded=%d", w.Code, *forwarded)
	}
}
