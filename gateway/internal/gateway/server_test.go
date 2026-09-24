package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cipherTing/sael/gateway/internal/policy"
)

type testClassifier struct {
	answers []policy.Answer
	err     error
	calls   int
}

func (c *testClassifier) Check(_ context.Context, _ string) ([]policy.Answer, error) {
	c.calls++
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
func (s *testStore) UpdatePolicy(_ context.Context, old int64, next policy.Policy, _ string) (policy.Policy, error) {
	if old != s.policy.Version {
		return policy.Policy{}, ErrConflict
	}
	next.Version = old + 1
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
func (s *testStore) Event(_ context.Context, id string) (Event, error) {
	for _, e := range s.events {
		if e.ID == id {
			return e, nil
		}
	}
	return Event{}, ErrNotFound
}
func (s *testStore) Changes(context.Context) ([]PolicyChange, error) { return nil, nil }

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
	return policy.Policy{Enabled: true, Version: 2, PreviewChars: &preview, RetentionDays: &days,
		Scenes: []policy.Scene{{ID: "block", Name: "block cyber", Conditions: []policy.Condition{{Question: "cyber_abuse", Threshold: 0.5}}, Match: policy.Any, Action: policy.Block}}}
}

func TestBlockedRequestDoesNotReachUpstream(t *testing.T) {
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": 0.9})}
	s, store, calls := makeServer(t, activePolicy(), c)
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"danger"}]}`))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 || *calls != 0 || len(store.events) != 1 || store.events[0].Decision.SceneID != "block" || store.events[0].TextPreview != "" {
		t.Fatalf("status=%d upstream=%d events=%+v", w.Code, *calls, store.events)
	}
	if len(store.counts) != 1 || store.counts[0].Outcome != "blocked" {
		t.Fatalf("counts=%+v", store.counts)
	}
}

func TestAllowedHitForwardsOriginalRequest(t *testing.T) {
	p := activePolicy()
	p.Scenes[0].Action = policy.Allow
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": 0.9})}
	s, store, calls := makeServer(t, p, c)
	body := `{"model":"test","stream":true,"messages":[{"role":"user","content":"danger"}]}`
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions?trace=1", strings.NewReader(body)))
	if w.Code != 201 || *calls != 1 || w.Header().Get("X-Upstream-Body") != body || w.Header().Get("X-Upstream-Query") != "trace=1" || len(store.events) != 1 || store.counts[0].Outcome != "hit_allowed" {
		t.Fatalf("status=%d calls=%d event=%+v counts=%+v", w.Code, *calls, store.events, store.counts)
	}
}

func TestNoCurrentUserTextBypassesClassifier(t *testing.T) {
	c := &testClassifier{err: errors.New("should not call")}
	s, store, calls := makeServer(t, activePolicy(), c)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"old"},{"role":"assistant","content":"reply"}]}`)))
	if w.Code != 201 || *calls != 1 || c.calls != 0 || len(store.events) != 0 || store.counts[0].Outcome != "no_text" {
		t.Fatalf("status=%d upstream=%d classifier=%d", w.Code, *calls, c.calls)
	}
}

func TestClassifierFailureIsVisibleButAllowsRequest(t *testing.T) {
	c := &testClassifier{err: errors.New("unavailable")}
	s, store, calls := makeServer(t, activePolicy(), c)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"new"}]}`)))
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
	s.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"new request"}]}`)))
	if w.Code != 201 || len(store.events) != 1 || store.events[0].TextPreview != "new request" {
		t.Fatalf("status=%d events=%+v", w.Code, store.events)
	}
}

func TestDisabledPolicyForwardsWithoutClassifying(t *testing.T) {
	c := &testClassifier{err: errors.New("should not call")}
	s, store, _ := makeServer(t, policy.Policy{Version: 1}, c)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"new"}]}`)))
	if w.Code != 201 || c.calls != 0 || store.counts[0].Outcome != "disabled" {
		t.Fatalf("status=%d calls=%d counts=%+v", w.Code, c.calls, store.counts)
	}
}

func TestUpstreamFailureDoesNotChangeIngressCount(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer upstream.Close()
	store := &testStore{policy: policy.Policy{Version: 1}, upstream: UpstreamConfig{BaseURL: upstream.URL}}
	s := New(store, &testClassifier{}, "secret")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}]}`)))
	if w.Code != 503 || len(store.counts) != 1 || store.counts[0].Outcome != "disabled" {
		t.Fatalf("status=%d counts=%+v", w.Code, store.counts)
	}
}

func TestReviewedRequestContributesClassifierLatency(t *testing.T) {
	s, store, _ := makeServer(t, activePolicy(), &testClassifier{answers: fullAnswers(nil)})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}]}`)))
	if w.Code != 201 || len(store.counts) != 1 || !store.counts[0].ClassifierSample {
		t.Fatalf("status=%d counts=%+v", w.Code, store.counts)
	}
}

func TestMalformedRequestIsCountedWithoutForwarding(t *testing.T) {
	c := &testClassifier{}
	s, store, calls := makeServer(t, activePolicy(), c)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":`)))
	if w.Code != 400 || *calls != 0 || c.calls != 0 || len(store.counts) != 1 || store.counts[0].Outcome != "invalid_json" {
		t.Fatalf("status=%d upstream=%d classifier=%d counts=%+v", w.Code, *calls, c.calls, store.counts)
	}
}

type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, errors.New("request body unavailable") }

func TestBodyReadFailureStillCountsTheIncomingRequest(t *testing.T) {
	s, store, calls := makeServer(t, activePolicy(), &testClassifier{})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", brokenBody{}))
	if w.Code != 400 || *calls != 0 || len(store.counts) != 1 || store.counts[0].Outcome != "request_error" {
		t.Fatalf("status=%d upstream=%d counts=%+v", w.Code, *calls, store.counts)
	}
}

func TestPolicyLoadFailureStillCountsTheIncomingRequest(t *testing.T) {
	s, store, calls := makeServer(t, activePolicy(), &testClassifier{})
	store.policyErr = errors.New("policy store unavailable")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"new"}]}`)))
	if w.Code != 503 || *calls != 0 || len(store.counts) != 1 || store.counts[0].Outcome != "gateway_error" {
		t.Fatalf("status=%d upstream=%d counts=%+v", w.Code, *calls, store.counts)
	}
}
