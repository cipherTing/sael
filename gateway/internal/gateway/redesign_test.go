package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cipherTing/sael/gateway/internal/policy"
)

type parallelClassifier struct {
	arrived chan struct{}
	release chan struct{}
}

func (c *parallelClassifier) Check(ctx context.Context, _ string) ([]policy.Answer, error) {
	c.arrived <- struct{}{}
	select {
	case <-c.release:
		return fullAnswers(map[string]float64{"cyber_abuse": 0.9}), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type parallelStore struct {
	*testStore
	mu sync.Mutex
}

func (s *parallelStore) WriteEvent(ctx context.Context, e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.testStore.WriteEvent(ctx, e)
}

func (s *parallelStore) Increment(ctx context.Context, c Count) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.testStore.Increment(ctx, c)
}

func TestClassifierHasNoImplicitThirtyTwoRequestLimit(t *testing.T) {
	c := &parallelClassifier{arrived: make(chan struct{}, 33), release: make(chan struct{})}
	s, store, _ := makeServer(t, activePolicy(), &testClassifier{})
	s.Classifier = c
	s.Store = &parallelStore{testStore: store}
	var wg sync.WaitGroup
	defer wg.Wait()
	defer close(c.release)
	for range 33 {
		wg.Go(func() {
			w := httptest.NewRecorder()
			s.ServeHTTP(w, authorizedRequest("POST", "/v1/responses", strings.NewReader(`{"input":"test"}`)))
		})
	}
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for i := range 33 {
		select {
		case <-c.arrived:
		case <-deadline.C:
			t.Fatalf("only %d requests reached the classifier; a gateway queue was imposed", i)
		}
	}
}

func TestSceneOnlyAffectsSelectedEndpoint(t *testing.T) {
	p := activePolicy()
	raw := `{"enabled":true,"version":2,"scenes":[{"id":"scope","name":"Responses only","endpoints":["openai_responses"],"conditions":[{"question":"gore","threshold":1.5}],"match":"all","action":"block"}]}`
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	c := &testClassifier{answers: fullAnswers(map[string]float64{"gore": 2})}
	s, store, _ := makeServer(t, p, c)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"test"}]}`)))
	if w.Code != 201 || len(store.events) != 0 || c.calls != 0 {
		t.Fatalf("unselected endpoint must pass without event: %d %+v", w.Code, store.events)
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/responses", strings.NewReader(`{"input":"test"}`)))
	if w.Code != 403 || len(store.events) != 1 || store.events[0].Endpoint != "/v1/responses" || c.calls != 1 {
		t.Fatalf("selected endpoint: %d %+v", w.Code, store.events)
	}
}

func TestDraftSimulationReusesScoresAndDoesNotPublish(t *testing.T) {
	c := &testClassifier{}
	s, store, upstream := makeServer(t, activePolicy(), c)
	draft := activePolicy()
	draft.Enabled = false
	draft.Scenes[0].Action = policy.Allow
	raw, _ := json.Marshal(map[string]any{"policy": draft, "endpoint": "openai_chat", "scores": fullAnswers(map[string]float64{"cyber_abuse": 0.9})})
	w := adminRequest(t, s, "POST", "/admin/policy/test", string(raw))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"action":"allow"`) || !strings.Contains(w.Body.String(), `"status":"effective"`) {
		t.Fatalf("draft simulation: %d %s", w.Code, w.Body.String())
	}
	if c.calls != 0 || *upstream != 0 || len(store.events) != 0 || len(store.counts) != 0 || store.policy.Scenes[0].Action != policy.Block {
		t.Fatal("simulation changed production or called external services")
	}
}

func TestDraftSimulationRejectsUnknownEndpoints(t *testing.T) {
	s, _, _ := makeServer(t, activePolicy(), &testClassifier{})
	w := adminRequest(t, s, "POST", "/admin/policy/test", `{"endpoint":"unknown","text":"hello"}`)
	if w.Code != 400 {
		t.Fatalf("unknown endpoint accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestJevConnectionCanBeTestedBeforeSaving(t *testing.T) {
	c := &testClassifier{answers: fullAnswers(nil)}
	s, store, _ := makeServer(t, activePolicy(), c)
	w := adminRequest(t, s, "POST", "/admin/jev/test", `{"text":"hello","connection":{"base_url":"https://example.test/v1","model":"test","api_key":"draft-key","timeout_ms":5000}}`)
	if w.Code != 200 || c.calls != 1 || store.jev.APIKey != "" {
		t.Fatalf("unsaved connection test: %d %s", w.Code, w.Body.String())
	}
}

func TestCleanRequestsKeepOnlyCountAndLatencyWithoutCreatingEvents(t *testing.T) {
	s, store, _ := makeServer(t, activePolicy(), &testClassifier{answers: fullAnswers(map[string]float64{"gore": 0.25})})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/responses", strings.NewReader(`{"input":"hello"}`)))
	raw, _ := json.Marshal(store.counts)
	if w.Code != 201 || len(store.events) != 0 || strings.Contains(string(raw), `"scores"`) || len(store.counts) != 1 || !store.counts[0].ClassifierSample {
		t.Fatalf("clean count and latency aggregation missing: %d %s", w.Code, raw)
	}
}

func TestPausedSceneDoesNotBlock(t *testing.T) {
	p := activePolicy()
	raw := `{"enabled":true,"scenes":[{"id":"off","name":"Paused","enabled":false,"conditions":[{"question":"gore","threshold":1}],"match":"any","action":"block"}]}`
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	s, store, _ := makeServer(t, p, &testClassifier{answers: fullAnswers(map[string]float64{"gore": 2})})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/responses", strings.NewReader(`{"input":"test"}`)))
	if w.Code != 201 || len(store.events) != 0 {
		t.Fatalf("paused scene affected traffic: %d %+v", w.Code, store.events)
	}
}

func TestIngressDoesNotExposeManagementAPI(t *testing.T) {
	s, _, _ := makeServer(t, activePolicy(), &testClassifier{})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/admin/login", strings.NewReader(`{"password":"secret"}`)))
	if w.Code != http.StatusNotFound || len(w.Result().Cookies()) != 0 {
		t.Fatalf("ingress exposed login: %d", w.Code)
	}
}

func TestRecordedTextContainsOnlyCurrentUserInput(t *testing.T) {
	s, store, _ := makeServer(t, activePolicy(), &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": 0.9})})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"system","content":"private history"},{"role":"user","content":"old text"},{"role":"assistant","content":"reply"},{"role":"user","content":"current text"}]}`)))
	raw, _ := json.Marshal(store.events)
	if !strings.Contains(string(raw), `"text":"current text"`) || strings.Contains(string(raw), "private history") || strings.Contains(string(raw), "old text") {
		t.Fatalf("recorded text: %s", raw)
	}
}

func TestUnmonitoredEndpointPassesThroughWithoutReview(t *testing.T) {
	c := &testClassifier{}
	s, store, _ := makeServer(t, activePolicy(), c)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("GET", "/v1/models?limit=2", http.NoBody))
	if w.Code != 201 || c.calls != 0 || len(store.counts) != 0 || w.Header().Get("X-Upstream-Query") != "limit=2" {
		t.Fatalf("passthrough: %d %+v", w.Code, store.counts)
	}
}

func TestInvalidClassificationHasItsOwnFailureReason(t *testing.T) {
	s, store, _ := makeServer(t, activePolicy(), &testClassifier{answers: fullAnswers(map[string]float64{"gore": 4})})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/responses", strings.NewReader(`{"input":"test"}`)))
	if len(store.events) != 1 || store.events[0].ErrorKind != "classifier_invalid_response" || store.counts[0].ErrorKind != "classifier_invalid_response" {
		t.Fatalf("invalid scores were not identified: %+v", store.events)
	}
}

type canceledClassifier struct{ cancel context.CancelFunc }

func (c canceledClassifier) Check(ctx context.Context, _ string) ([]policy.Answer, error) {
	c.cancel()
	return nil, ctx.Err()
}

func TestClientCancellationIsNotReportedAsJevFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, store, _ := makeServer(t, activePolicy(), &testClassifier{})
	s.Classifier = canceledClassifier{cancel: cancel}
	w := httptest.NewRecorder()
	r := authorizedRequest("POST", "/v1/responses", strings.NewReader(`{"input":"test"}`)).WithContext(ctx)
	s.ServeHTTP(w, r)
	if len(store.events) != 0 || len(store.counts) != 1 || store.counts[0].ErrorKind != "" {
		t.Fatalf("client cancellation was treated as a classifier error: %+v", store.events)
	}
}

func TestRelativeAnalyticsRangeContainsExactlyTheSelectedMinutes(t *testing.T) {
	f, err := analyticsFilter(url.Values{"minutes": {"60"}})
	if err != nil {
		t.Fatal(err)
	}
	if f.Until.Sub(f.Since) != time.Hour {
		t.Fatalf("one-hour selection became %s", f.Until.Sub(f.Since))
	}
	if f.Since.Second() != 0 || f.Until.Second() != 0 {
		t.Fatal("range is not minute aligned")
	}
}
