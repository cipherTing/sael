package gateway

import (
	"context"
	"errors"
	"github.com/cipherTing/sael/gateway/internal/policy"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type securityTestStore struct {
	*testStore
	mu       sync.Mutex
	keys     map[string]bool
	trustErr error
	retry    time.Duration
	counted  chan Count
}

func (s *securityTestStore) LoginAttempt(context.Context, string) (time.Duration, error) {
	return s.retry, nil
}
func (s *securityTestStore) TrustedKey(_ context.Context, key string, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.keys[key], s.trustErr
}
func (s *securityTestStore) RememberKey(_ context.Context, key string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[key] = true
	return nil
}
func (s *securityTestStore) ForgetKey(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.keys, key)
	return nil
}
func (s *securityTestStore) Increment(ctx context.Context, c Count) error {
	if s.counted != nil {
		s.counted <- c
	}
	return nil
}
func (s *securityTestStore) WriteEvent(ctx context.Context, e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.testStore.WriteEvent(ctx, e)
}
func admissionRequest(body string) *http.Request {
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer caller-secret")
	return r
}
func securityServer(t *testing.T, p policy.Policy, c Classifier, status *int) (*Server, *securityTestStore) {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(*status)
		_, _ = io.WriteString(w, `{"id":"resp_test","output":[]}`)
	}))
	t.Cleanup(up.Close)
	st := &securityTestStore{testStore: &testStore{policy: p, upstream: UpstreamConfig{BaseURL: up.URL}}, keys: map[string]bool{}}
	s := New(st, c, "secret")
	t.Cleanup(s.Close)
	return s, st
}
func TestFirstSuccessLearnsKeyWithoutReviewAndNextRequestIsReviewed(t *testing.T) {
	status := 200
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": .9})}
	s, st := securityServer(t, activePolicy(), c, &status)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, admissionRequest(`{"input":"first"}`))
	if w.Code != 200 || c.calls != 0 || len(st.events) != 0 {
		t.Fatalf("cold key must relay without review: %d calls=%d", w.Code, c.calls)
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, admissionRequest(`{"input":"second"}`))
	if w.Code != 403 || c.calls != 1 || len(st.events) != 1 {
		t.Fatalf("trusted key not reviewed: %d calls=%d", w.Code, c.calls)
	}
}
func TestRejectedKeyNeverEntersReview(t *testing.T) {
	status := 401
	c := &testClassifier{answers: fullAnswers(nil)}
	s, st := securityServer(t, activePolicy(), c, &status)
	for range 3 {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, admissionRequest(`not JSON`))
		if w.Code != 401 {
			t.Fatalf("cold request should stay transparent: %d", w.Code)
		}
	}
	if c.calls != 0 || len(st.keys) != 0 {
		t.Fatal("invalid credentials created review work or trusted state")
	}
}
func TestUnneededReviewDoesNotDecodeBody(t *testing.T) {
	for _, mode := range []string{"disabled", "endpoint", "paused"} {
		t.Run(mode, func(t *testing.T) {
			p := activePolicy()
			switch mode {
			case "disabled":
				p.Enabled = false
			case "endpoint":
				p.Scenes[0].Endpoints = []string{"anthropic"}
			case "paused":
				b := false
				p.Scenes[0].Enabled = &b
			}
			status := 200
			s, st := securityServer(t, p, &testClassifier{}, &status)
			s.ServeHTTP(httptest.NewRecorder(), admissionRequest(`{"input":"warm"}`))
			if len(st.keys) != 1 {
				t.Fatal("warm request did not establish trust")
			}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, admissionRequest(`not JSON`))
			if w.Code != 200 {
				t.Fatalf("unneeded review decoded body: %d", w.Code)
			}
		})
	}
}
func TestLoginHonorsServerCooldownBeforeDecoding(t *testing.T) {
	status := 200
	s, st := securityServer(t, activePolicy(), &testClassifier{}, &status)
	st.retry = time.Minute
	w := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(w, httptest.NewRequest("POST", "/admin/login", strings.NewReader(`not JSON`)))
	if w.Code != 429 || w.Header().Get("Retry-After") != "60" {
		t.Fatalf("cooldown: %d %s", w.Code, w.Header().Get("Retry-After"))
	}
}

type gatedClassifier struct {
	started chan struct{}
	release chan struct{}
}

func (c *gatedClassifier) Check(ctx context.Context, _ string) ([]policy.Answer, error) {
	close(c.started)
	select {
	case <-c.release:
		return fullAnswers(map[string]float64{"cyber_abuse": .9}), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func TestNonblockingReviewOutlivesClientResponse(t *testing.T) {
	status := 200
	p := activePolicy()
	p.Scenes[0].Action = policy.Allow
	c := &gatedClassifier{make(chan struct{}), make(chan struct{})}
	s, st := securityServer(t, p, c, &status)
	coldDone := make(chan struct{})
	go func() { s.ServeHTTP(httptest.NewRecorder(), admissionRequest(`{"input":"first"}`)); close(coldDone) }()
	select {
	case <-coldDone:
	case <-time.After(time.Second):
		close(c.release)
		t.Fatal("cold request was reviewed")
	}
	st.counted = make(chan Count, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := admissionRequest(`{"input":"parallel"}`).WithContext(ctx)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { s.ServeHTTP(w, r); close(done) }()
	select {
	case <-c.started:
	case <-time.After(time.Second):
		close(c.release)
		t.Fatal("review never started")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		close(c.release)
		t.Fatal("classifier delayed upstream response")
	}
	if w.Code != 200 {
		close(c.release)
		t.Fatalf("response=%d", w.Code)
	}
	cancel()
	close(c.release)
	select {
	case count := <-st.counted:
		if count.Outcome != "hit_allowed" {
			t.Fatalf("detached review lost result: %+v", count)
		}
	case <-time.After(time.Second):
		t.Fatal("review stopped with client context")
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.events) != 1 || st.events[0].Kind != "hit" {
		t.Fatalf("review event=%+v", st.events)
	}
}

func TestBodyLimitRejectsKnownAndChunkedOversize(t *testing.T) {
	status := 200
	s, _ := securityServer(t, activePolicy(), &testClassifier{}, &status)
	s.MaxBodyBytes = 32
	for _, known := range []bool{true, false} {
		r := admissionRequest(strings.Repeat("x", 33))
		if !known {
			r.ContentLength = -1
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 413 {
			t.Fatalf("known=%v oversized body status=%d", known, w.Code)
		}
	}
}
func TestTrustIsScopedToDestinationAndRevokedOnUnauthorized(t *testing.T) {
	status := 200
	c := &testClassifier{answers: fullAnswers(nil)}
	s, st := securityServer(t, activePolicy(), c, &status)
	for range 2 {
		s.ServeHTTP(httptest.NewRecorder(), admissionRequest(`{"input":"hello"}`))
	}
	if c.calls != 1 {
		t.Fatalf("expected one trusted review, got %d", c.calls)
	}
	status = 401
	s.ServeHTTP(httptest.NewRecorder(), admissionRequest(`{"input":"expired"}`))
	s.ServeHTTP(httptest.NewRecorder(), admissionRequest(`{"input":"expired again"}`))
	if c.calls != 2 || len(st.keys) != 0 {
		t.Fatalf("revoked key reviewed again: %d", c.calls)
	}
	status = 200
	s.ServeHTTP(httptest.NewRecorder(), admissionRequest(`{"input":"learn again"}`))
	old := st.upstream.BaseURL
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"new"}`)
	}))
	defer other.Close()
	st.upstream.BaseURL = other.URL
	s.ServeHTTP(httptest.NewRecorder(), admissionRequest(`{"input":"new target"}`))
	if c.calls != 2 {
		t.Fatal("credential trust crossed upstreams", old, other.URL)
	}
}
func TestPublicOrHTMLResponseDoesNotTeachCredential(t *testing.T) {
	status := 200
	c := &testClassifier{}
	s, st := securityServer(t, activePolicy(), c, &status)
	r := admissionRequest(`not JSON`)
	r.Method = "GET"
	r.URL.Path = "/models"
	s.ServeHTTP(httptest.NewRecorder(), r)
	if len(st.keys) != 0 {
		t.Fatal("public endpoint taught a credential")
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html>login</html>")
	}))
	defer up.Close()
	st.upstream.BaseURL = up.URL
	s.ServeHTTP(httptest.NewRecorder(), admissionRequest(`anything`))
	if len(st.keys) != 0 {
		t.Fatal("HTML success taught a credential")
	}
}

func TestMixedSceneModesWaitAndKeepPriority(t *testing.T) {
	p := activePolicy()
	p.Scenes = append([]policy.Scene{{ID: "observe", Name: "Observe first", Action: policy.Allow, Match: policy.Any, Conditions: []policy.Condition{{Question: "cyber_abuse", Threshold: .5}}}}, p.Scenes...)
	status := 200
	c := &gatedClassifier{make(chan struct{}), make(chan struct{})}
	s, st := securityServer(t, p, c, &status)
	s.ServeHTTP(httptest.NewRecorder(), admissionRequest(`{"input":"warm"}`))
	st.counted = make(chan Count, 1)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { s.ServeHTTP(w, admissionRequest(`{"input":"mixed"}`)); close(done) }()
	select {
	case <-c.started:
	case <-time.After(time.Second):
		close(c.release)
		t.Fatal("review did not start")
	}
	select {
	case <-done:
		close(c.release)
		t.Fatal("possible blocking scene was ignored")
	case <-time.After(30 * time.Millisecond):
	}
	close(c.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("request did not finish")
	}
	if w.Code != 200 || (<-st.counted).Outcome != "hit_allowed" {
		t.Fatalf("scene priority changed: %d", w.Code)
	}
}
func TestBusyNonblockingReviewDoesNotDelayRelay(t *testing.T) {
	p := activePolicy()
	p.Scenes[0].Action = policy.Allow
	status := 200
	c := &gatedClassifier{make(chan struct{}), make(chan struct{})}
	s, st := securityServer(t, p, c, &status)
	s.AsyncReviewConcurrency = 1
	s.ServeHTTP(httptest.NewRecorder(), admissionRequest(`{"input":"warm"}`))
	st.counted = make(chan Count, 2)
	s.ServeHTTP(httptest.NewRecorder(), admissionRequest(`{"input":"first"}`))
	select {
	case <-c.started:
	case <-time.After(time.Second):
		close(c.release)
		t.Fatal("review did not start")
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, admissionRequest(`{"input":"second"}`))
	if w.Code != 200 || (<-st.counted).Outcome != "review_busy" {
		close(c.release)
		t.Fatal("full review pool delayed or lost ingress")
	}
	close(c.release)
	s.reviewWG.Wait()
	if (<-st.counted).Outcome != "hit_allowed" {
		t.Fatal("active review did not finish")
	}
}

func TestShutdownDrainsNonblockingReview(t *testing.T) {
	p := activePolicy()
	p.Scenes[0].Action = policy.Allow
	status := 200
	c := &gatedClassifier{make(chan struct{}), make(chan struct{})}
	s, st := securityServer(t, p, c, &status)
	s.ServeHTTP(httptest.NewRecorder(), admissionRequest(`{"input":"warm"}`))
	st.counted = make(chan Count, 1)
	s.ServeHTTP(httptest.NewRecorder(), admissionRequest(`{"input":"review"}`))
	<-c.started
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-done:
		close(c.release)
		t.Fatal("shutdown canceled an active review immediately")
	case <-time.After(20 * time.Millisecond):
	}
	close(c.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish after review")
	}
	if count := <-st.counted; count.Outcome != "hit_allowed" {
		t.Fatal("shutdown discarded the review", count.Outcome)
	}
}

func TestTrustedBearerIsNotChangedByUnusedCredentialHeader(t *testing.T) {
	status := 200
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": .9})}
	s, _ := securityServer(t, activePolicy(), c, &status)
	s.ServeHTTP(httptest.NewRecorder(), admissionRequest(`{"input":"warm"}`))
	r := admissionRequest(`{"input":"review"}`)
	r.Header.Set("X-Api-Key", "irrelevant-value")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 || c.calls != 1 {
		t.Fatalf("extra credential header bypassed trust: %d calls=%d", w.Code, c.calls)
	}
}

func TestUnavailableCredentialStoreStillCountsIngress(t *testing.T) {
	status := 200
	c := &testClassifier{}
	s, st := securityServer(t, activePolicy(), c, &status)
	st.trustErr = errors.New("unavailable")
	st.counted = make(chan Count, 1)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, admissionRequest(`{"input":"hello"}`))
	if w.Code != 503 || c.calls != 0 {
		t.Fatalf("unavailable credential store: %d calls=%d", w.Code, c.calls)
	}
	select {
	case count := <-st.counted:
		if count.Outcome != "gateway_error" {
			t.Fatal("wrong ingress outcome", count.Outcome)
		}
	default:
		t.Fatal("rejected ingress missing from counters")
	}
}
