package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cipherTing/sael/gateway/internal/policy"
)

type testClassifier struct {
	answers []policy.Answer
	err     error
	calls   int
	text    string
}

type subsetClassifier struct {
	requested [][]string
}

func (*subsetClassifier) Check(context.Context, string) ([]policy.Answer, error) {
	return nil, errors.New("gateway called the unconfigured classifier")
}

func (c *subsetClassifier) CheckConfigured(_ context.Context, _ string, _ JevConfig, keys []string) ([]policy.Answer, error) {
	c.requested = append(c.requested, append([]string(nil), keys...))
	answers := make([]policy.Answer, 0, len(keys))
	for _, key := range keys {
		for _, question := range policy.Questions {
			if question.Key == key {
				value := 0.0
				if key == "gore" {
					value = 1.6
				}
				answers = append(answers, policy.Answer{Question: key, Type: question.Type, Value: value})
			}
		}
	}
	return answers, nil
}

func (c *testClassifier) Check(_ context.Context, text string) ([]policy.Answer, error) {
	c.calls++
	c.text = text
	return c.answers, c.err
}

type testStore struct {
	policy        policy.Policy
	policyErr     error
	jev           JevConfig
	upstream      UpstreamConfig
	events        []Event
	counts        []Count
	overviewSince time.Time
	overviewUntil time.Time
	blocked       map[string]time.Time
	sessionErr    error
	adminSessions map[string]time.Time
}

func (s *testStore) Jev(context.Context) (JevConfig, error)           { return s.jev, nil }
func (s *testStore) Upstream(context.Context) (UpstreamConfig, error) { return s.upstream, nil }
func (s *testStore) UpdateUpstream(_ context.Context, next UpstreamConfig) (UpstreamConfig, error) {
	s.upstream = next
	return next, nil
}
func (s *testStore) UpdateJev(_ context.Context, next JevConfig) (JevConfig, error) {
	s.jev = next
	return next, nil
}

func (s *testStore) Policy(context.Context) (policy.Policy, error) { return s.policy, s.policyErr }
func (s *testStore) UpdatePolicy(_ context.Context, update PolicyUpdate, _ string) (policy.Policy, error) {
	next, err := update.Apply(s.policy)
	if err != nil {
		return policy.Policy{}, err
	}
	s.policy = next
	return next, nil
}
func (s *testStore) WriteEvent(_ context.Context, e Event) error {
	s.events = append(s.events, e)
	return nil
}
func (s *testStore) Increment(_ context.Context, c Count) error {
	s.counts = append(s.counts, c)
	return nil
}
func (s *testStore) Overview(_ context.Context, since, until time.Time) (Overview, error) {
	s.overviewSince, s.overviewUntil = since, until
	return Overview{}, nil
}
func (s *testStore) Events(context.Context, EventFilter) ([]Event, error) { return s.events, nil }
func (s *testStore) Analytics(_ context.Context, f AnalyticsFilter) (Analytics, error) {
	return Analytics{Since: f.Since, Until: f.Until}, nil
}
func (s *testStore) Event(_ context.Context, id string) (Event, error) {
	for _, e := range s.events {
		if e.ID == id {
			return e, nil
		}
	}
	return Event{}, ErrNotFound
}
func (s *testStore) Changes(context.Context) ([]PolicyChange, error) { return nil, nil }
func (s *testStore) PutSessionBlock(_ context.Context, key string, until time.Time) error {
	if s.sessionErr != nil {
		return s.sessionErr
	}
	if s.blocked == nil {
		s.blocked = map[string]time.Time{}
	}
	s.blocked[key] = until
	return nil
}
func (s *testStore) SessionBlockActive(_ context.Context, key string, now time.Time) (bool, error) {
	if s.sessionErr != nil {
		return false, s.sessionErr
	}
	return s.blocked[key].After(now), nil
}

// Existing rule tests start with an already trusted caller. Admission tests use an empty trust store.
func (s *testStore) LoginAttempt(context.Context, string) (time.Duration, error) { return 0, nil }
func (s *testStore) PutAdminSession(_ context.Context, token string, ttl time.Duration) error {
	if s.adminSessions == nil {
		s.adminSessions = make(map[string]time.Time)
	}
	s.adminSessions[token] = time.Now().Add(ttl)
	return nil
}
func (s *testStore) AdminSessionActive(_ context.Context, token string) (bool, error) {
	return time.Now().Before(s.adminSessions[token]), nil
}
func (s *testStore) DeleteAdminSession(_ context.Context, token string) error {
	delete(s.adminSessions, token)
	return nil
}
func (s *testStore) TrustedKey(_ context.Context, key string, _ time.Duration) (bool, error) {
	return key != "", nil
}
func (s *testStore) RememberKey(context.Context, string, time.Duration) error { return nil }
func (s *testStore) ForgetKey(context.Context, string) error                  { return nil }
func authorizedRequest(method, target string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, target, body)
	if strings.HasPrefix(target, "/v1/") {
		r.Header.Set("Authorization", "Bearer test-key")
	}
	return r
}

func makeServer(t *testing.T, p policy.Policy, classifier *testClassifier) (*Server, *testStore, *int) {
	t.Helper()
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Upstream-Body", string(body))
		w.Header().Set("X-Upstream-Query", r.URL.RawQuery)
		w.WriteHeader(201)
		_, _ = w.Write([]byte("upstream"))
	}))
	t.Cleanup(upstream.Close)
	store := &testStore{policy: p, upstream: UpstreamConfig{BaseURL: upstream.URL}}
	server := New(store, classifier, "secret")
	t.Cleanup(server.Close)
	return server, store, &calls
}

func fullAnswers(values map[string]float64) []policy.Answer {
	out := make([]policy.Answer, 0, len(policy.Questions))
	for _, q := range policy.Questions {
		out = append(out, policy.Answer{Question: q.Key, Type: q.Type, Value: values[q.Key]})
	}
	return out
}

func activePolicy() policy.Policy {
	preview, days := 0, 30
	return policy.Policy{Enabled: true, PreviewChars: &preview, RetentionDays: &days,
		Scenes: []policy.Scene{{ID: "block", Name: "block cyber", Conditions: []policy.Condition{{Question: "cyber_abuse", Threshold: 0.5}}, Match: policy.Any, Action: policy.Block}}}
}

func TestGatewayRequestsOnlyQuestionsFromApplicableScenes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(201) }))
	defer upstream.Close()
	p := activePolicy()
	p.Scenes = []policy.Scene{
		{ID: "one", Name: "one", Match: policy.All, Action: policy.Block, Endpoints: []string{"openai_chat"}, Models: []string{"selected"}, Conditions: []policy.Condition{{Question: "gore", Threshold: 1.5}, {Question: "self_harm", Threshold: 0.8}}},
		{ID: "two", Name: "two", Match: policy.Any, Action: policy.Allow, Endpoints: []string{"openai_chat"}, Models: []string{"selected"}, Conditions: []policy.Condition{{Question: "self_harm", Threshold: 0.9}, {Question: "cyber_abuse", Threshold: 0.8}}},
		{ID: "other", Name: "other", Match: policy.Any, Action: policy.Block, Endpoints: []string{"openai_responses"}, Conditions: []policy.Condition{{Question: "sexual", Threshold: 1}}},
	}
	store := &testStore{policy: p, upstream: UpstreamConfig{BaseURL: upstream.URL}, jev: JevConfig{BaseURL: "https://jev.example/v1", APIKey: "test-key", Model: "jev"}}
	classifier := &subsetClassifier{}
	s := New(store, classifier, "secret")
	defer s.Close()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"selected","messages":[{"role":"user","content":"prompt"}]}`)))
	if w.Code != 201 || len(classifier.requested) != 1 {
		t.Fatalf("status=%d requests=%v", w.Code, classifier.requested)
	}
	want := []string{"cyber_abuse", "self_harm", "gore"}
	if !slices.Equal(classifier.requested[0], want) || len(store.events) != 0 {
		t.Fatalf("requested=%v events=%+v", classifier.requested, store.events)
	}
}

func TestBlockedRequestDoesNotReachUpstream(t *testing.T) {
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": 0.9})}
	s, store, calls := makeServer(t, activePolicy(), c)
	r := authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"danger"}]}`))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 || *calls != 0 || len(store.events) != 1 || store.events[0].Decision.SceneID != "block" || store.events[0].TextPreview != "" {
		t.Fatalf("status=%d upstream=%d events=%+v", w.Code, *calls, store.events)
	}
	if len(store.counts) != 1 || store.counts[0].Outcome != "blocked" {
		t.Fatalf("counts=%+v", store.counts)
	}
}

func TestBlockedResponseUsesProtocolEnvelopeWithoutRiskDetails(t *testing.T) {
	for _, tc := range []struct {
		path, body, protocol string
	}{
		{"/v1/chat/completions", `{"model":"test","messages":[{"role":"user","content":"danger"}]}`, "openai_chat"},
		{"/v1/responses", `{"model":"test","input":"danger"}`, "openai_responses"},
		{"/v1/messages", `{"model":"test","messages":[{"role":"user","content":"danger"}]}`, "anthropic"},
		{"/v1/images/generations", `{"model":"gpt-image-1","prompt":"danger"}`, "openai_images_generations"},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": 0.9})}
			s, _, calls := makeServer(t, activePolicy(), c)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, authorizedRequest("POST", tc.path, strings.NewReader(tc.body)))
			if w.Code != http.StatusForbidden || *calls != 0 || strings.Contains(w.Body.String(), "danger") || strings.Contains(w.Body.String(), "cyber_abuse") {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, *calls, w.Body.String())
			}
			if tc.protocol == "anthropic" {
				if !strings.Contains(w.Body.String(), `"type":"error"`) || !strings.Contains(w.Body.String(), `"permission_error"`) {
					t.Fatalf("anthropic envelope: %s", w.Body.String())
				}
			} else if !strings.Contains(w.Body.String(), `"prompt_guard_blocked"`) {
				t.Fatalf("openai envelope: %s", w.Body.String())
			}
		})
	}
}

func TestUnmonitoredEndpointForwardsWithoutCallingClassifier(t *testing.T) {
	c := &testClassifier{answers: fullAnswers(nil)}
	s, store, calls := makeServer(t, activePolicy(), c)
	body := `{"model":"text-embedding-3-small","input":"hello"}`
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/embeddings", strings.NewReader(body)))
	if w.Code != 201 || *calls != 1 || c.calls != 0 || len(store.events) != 0 || len(store.counts) != 0 {
		t.Fatalf("status=%d upstream=%d classifier=%d events=%+v counts=%+v", w.Code, *calls, c.calls, store.events, store.counts)
	}
}

func TestImageGenerationIsClassifiedAndForwardedUnchanged(t *testing.T) {
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": 0.1})}
	s, store, calls := makeServer(t, activePolicy(), c)
	body := `{"model":"gpt-image-1","prompt":"draw a lake","size":"1024x1024"}`
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/images/generations", strings.NewReader(body)))
	if w.Code != 201 || *calls != 1 || c.calls != 1 || len(store.counts) != 1 || store.events != nil {
		t.Fatalf("status=%d upstream=%d classifier=%d events=%+v counts=%+v", w.Code, *calls, c.calls, store.events, store.counts)
	}
}

func TestPromptOverConfiguredJevLimitIsWarnedAndForwarded(t *testing.T) {
	p := activePolicy()
	c := &testClassifier{answers: fullAnswers(nil)}
	s, store, calls := makeServer(t, p, c)
	store.jev.MaxInputTokens = 3
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"1234567890"}]}`)))
	if w.Code != 201 || *calls != 1 || c.calls != 0 || len(store.events) != 1 || store.events[0].Kind != "warning" || store.events[0].ErrorKind != "classifier_input_too_long" || store.counts[0].Outcome != "input_too_long" {
		t.Fatalf("status=%d upstream=%d classifier=%d events=%+v counts=%+v", w.Code, *calls, c.calls, store.events, store.counts)
	}
}

func TestBlockedSessionIsRejectedBeforeClassifier(t *testing.T) {
	p := activePolicy()
	p.Scenes[0].SessionBlockEnabled = true
	p.Scenes[0].SessionBlockTTLSeconds = 600
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": 0.9})}
	s, store, calls := makeServer(t, p, c)
	body := `{"model":"test","messages":[{"role":"user","content":"danger"}]}`
	first := authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	first.Header.Set("X-Session-Id", "session-1")
	first.Header.Set("Authorization", "Bearer test-user")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, first)
	if w.Code != 403 || *calls != 0 || c.calls != 1 || len(store.blocked) < 1 {
		t.Fatalf("first status=%d upstream=%d classifier=%d blocks=%v", w.Code, *calls, c.calls, store.blocked)
	}
	second := authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"safe"}]}`))
	second.Header.Set("X-Session-Id", "session-1")
	second.Header.Set("Authorization", "Bearer test-user")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, second)
	if w.Code != 403 || *calls != 0 || c.calls != 1 || len(store.events) != 2 || store.events[1].Kind != "warning" || store.events[1].ErrorKind != "session_blocked" {
		t.Fatalf("second status=%d upstream=%d classifier=%d events=%+v", w.Code, *calls, c.calls, store.events)
	}
}

func TestAllowedHitForwardsOriginalRequest(t *testing.T) {
	p := activePolicy()
	p.Scenes[0].Action = policy.Allow
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": 0.9})}
	s, store, calls := makeServer(t, p, c)
	body := `{"model":"test","stream":true,"messages":[{"role":"user","content":"danger"}]}`
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/chat/completions?trace=1", strings.NewReader(body)))
	s.reviewWG.Wait()
	if w.Code != 201 || *calls != 1 || w.Header().Get("X-Upstream-Body") != body || w.Header().Get("X-Upstream-Query") != "trace=1" || len(store.events) != 1 || store.counts[0].Outcome != "hit_allowed" {
		t.Fatalf("status=%d calls=%d event=%+v counts=%+v", w.Code, *calls, store.events, store.counts)
	}
}

func TestNonBlockingReviewHitFreezesSessionAfterForwarding(t *testing.T) {
	p := activePolicy()
	p.Scenes[0].Action = policy.Allow
	p.Scenes[0].SessionBlockEnabled = true
	p.Scenes[0].SessionBlockTTLSeconds = 600
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": 0.9})}
	s, store, calls := makeServer(t, p, c)
	body := `{"model":"test","messages":[{"role":"user","content":"danger"}]}`
	first := authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	first.Header.Set("X-Session-Id", "session-async")
	first.Header.Set("Authorization", "Bearer test-user")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, first)
	s.reviewWG.Wait()
	if w.Code != 201 || *calls != 1 || len(store.events) != 1 || store.events[0].Kind != "hit" || len(store.blocked) < 1 {
		t.Fatalf("first status=%d upstream=%d events=%+v blocks=%v", w.Code, *calls, store.events, store.blocked)
	}
	second := authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"safe"}]}`))
	second.Header.Set("X-Session-Id", "session-async")
	second.Header.Set("Authorization", "Bearer test-user")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, second)
	if w.Code != 403 || *calls != 1 || c.calls != 1 {
		t.Fatalf("second status=%d upstream=%d classifier=%d events=%+v", w.Code, *calls, c.calls, store.events)
	}
}

func TestNoCurrentUserTextBypassesClassifier(t *testing.T) {
	c := &testClassifier{err: errors.New("should not call")}
	s, store, calls := makeServer(t, activePolicy(), c)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"old"},{"role":"assistant","content":"reply"}]}`)))
	if w.Code != 201 || *calls != 1 || c.calls != 0 || len(store.events) != 0 || len(store.counts) != 0 {
		t.Fatalf("status=%d upstream=%d classifier=%d", w.Code, *calls, c.calls)
	}
}

func TestIngressStatsCountOnlyCurrentUserInput(t *testing.T) {
	s, store, calls := makeServer(t, activePolicy(), &testClassifier{answers: fullAnswers(nil)})
	capture := &ingressCaptureStore{testStore: store}
	s.Store = capture

	assistantOnly := authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"old"},{"role":"assistant","content":"reply"}]}`))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, assistantOnly)
	if w.Code != 201 || *calls != 1 || len(capture.ingress) != 0 || len(store.counts) != 0 {
		t.Fatalf("assistant/tool request affected ingress stats: status=%d calls=%d ingress=%+v counts=%+v", w.Code, *calls, capture.ingress, store.counts)
	}

	userInput := authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"current"}]}`))
	w = httptest.NewRecorder()
	s.ServeHTTP(w, userInput)
	if w.Code != 201 || *calls != 2 || len(capture.ingress) != 1 || len(store.counts) != 1 {
		t.Fatalf("current user input was not counted once: status=%d calls=%d ingress=%+v counts=%+v", w.Code, *calls, capture.ingress, store.counts)
	}
}

func TestClassifierFailureIsVisibleButAllowsRequest(t *testing.T) {
	c := &testClassifier{err: errors.New("unavailable")}
	s, store, calls := makeServer(t, activePolicy(), c)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"new"}]}`)))
	if w.Code != 201 || *calls != 1 || len(store.events) != 1 || store.events[0].Kind != "failure" || store.counts[0].Outcome != "unreviewed" {
		t.Fatalf("status=%d upstream=%d events=%+v counts=%+v", w.Code, *calls, store.events, store.counts)
	}
}

func TestClassifierFailureKeepsCurrentUserTextPreviewWhenConfigured(t *testing.T) {
	p := activePolicy()
	preview := 20
	p.PreviewChars = &preview
	c := &testClassifier{err: errors.New("unavailable")}
	s, store, _ := makeServer(t, p, c)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"new request"}]}`)))
	if w.Code != 201 || len(store.events) != 1 || store.events[0].TextPreview != "new request" {
		t.Fatalf("status=%d events=%+v", w.Code, store.events)
	}
}

func TestDisabledPolicyForwardsWithoutClassifying(t *testing.T) {
	c := &testClassifier{err: errors.New("should not call")}
	s, store, _ := makeServer(t, policy.Policy{}, c)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"new"}]}`)))
	if w.Code != 201 || c.calls != 0 || len(store.counts) != 0 {
		t.Fatalf("status=%d calls=%d counts=%+v", w.Code, c.calls, store.counts)
	}
}

func TestDisabledUpstreamFailureIsNotCountedWithoutUserInputClassification(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer upstream.Close()
	store := &testStore{policy: policy.Policy{}, upstream: UpstreamConfig{BaseURL: upstream.URL}}
	s := New(store, &testClassifier{}, "secret")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}]}`)))
	if w.Code != 503 || len(store.counts) != 0 {
		t.Fatalf("status=%d counts=%+v", w.Code, store.counts)
	}
}

func TestReviewedRequestContributesClassifierLatency(t *testing.T) {
	s, store, _ := makeServer(t, activePolicy(), &testClassifier{answers: fullAnswers(nil)})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}]}`)))
	if w.Code != 201 || len(store.counts) != 1 || !store.counts[0].ClassifierSample {
		t.Fatalf("status=%d counts=%+v", w.Code, store.counts)
	}
}

func TestMalformedRequestIsNotCountedWithoutUserInput(t *testing.T) {
	c := &testClassifier{}
	s, store, calls := makeServer(t, activePolicy(), c)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":`)))
	if w.Code != 400 || *calls != 0 || c.calls != 0 || len(store.counts) != 0 {
		t.Fatalf("status=%d upstream=%d classifier=%d counts=%+v", w.Code, *calls, c.calls, store.counts)
	}
}

type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, errors.New("request body unavailable") }

func TestBodyReadFailureIsNotCountedWithoutUserInput(t *testing.T) {
	s, store, calls := makeServer(t, activePolicy(), &testClassifier{})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/chat/completions", brokenBody{}))
	if w.Code != 400 || *calls != 0 || len(store.counts) != 0 {
		t.Fatalf("status=%d upstream=%d counts=%+v", w.Code, *calls, store.counts)
	}
}

func TestPolicyLoadFailureIsNotCountedWithoutUserInput(t *testing.T) {
	s, store, calls := makeServer(t, activePolicy(), &testClassifier{})
	store.policyErr = errors.New("policy store unavailable")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"new"}]}`)))
	if w.Code != 503 || *calls != 0 || len(store.counts) != 0 {
		t.Fatalf("status=%d upstream=%d counts=%+v", w.Code, *calls, store.counts)
	}
}
