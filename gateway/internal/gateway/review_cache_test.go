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

func TestConditionCacheRecomputesDispositionAndMatchWithoutScores(t *testing.T) {
	var p policy.Policy
	_ = json.Unmarshal([]byte(`{"enabled":true,"scenes":[{"id":"mixed","name":"mixed","review_mode":"blocking","match":"any","action":"block","conditions":[{"question":"cyber_abuse","threshold":0.5,"record_only":true},{"question":"illicit","threshold":0.5}]}]}`), &p)
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": .9})}
	s, st, forwarded := makeServer(t, p, c)
	cache := &memoryReviewCache{values: map[string]bool{}}
	s.ReviewCache = cache
	send := func() int {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, authorizedRequest("POST", "/v1/responses", strings.NewReader(`{"input":"cached mixed hit"}`)))
		return w.Code
	}
	for range 2 {
		if send() != 201 {
			t.Fatal("record-only cached hit was rejected")
		}
	}
	if c.calls != 1 || len(cache.values) != 2 || len(st.events) != 2 {
		t.Fatalf("missing cached true/false facts: calls=%d values=%v", c.calls, cache.values)
	}
	if st.events[1].Decision.Hits[0].Value != nil || st.events[1].Trace[0].Conditions[0].Value != nil || len(st.events[1].Scores) != 0 {
		t.Fatal("cache fabricated a score")
	}
	st.policy.Scenes[0].Conditions[0].RecordOnly = false
	if send() != 403 || c.calls != 1 || st.events[2].Decision.Reason != "reject" {
		t.Fatal("cache retained record-only disposition")
	}
	st.policy.Scenes[0].Match = policy.All
	if send() != 201 || c.calls != 1 || *forwarded != 3 {
		t.Fatal("match change did not reuse false condition fact")
	}
	st.policy.Scenes[0].Conditions[0].Threshold = .95
	if send() != 201 || c.calls != 2 {
		t.Fatal("threshold change reused old predicate")
	}
}

func TestBlockingRecordOnlyDoesNotFreezeAndOnlyCountsFirstScene(t *testing.T) {
	var p policy.Policy
	_ = json.Unmarshal([]byte(`{"enabled":true,"scenes":[{"id":"observe","name":"observe","review_mode":"blocking","match":"all","action":"allow","conditions":[{"question":"cyber_abuse","threshold":0.5}]},{"id":"later","name":"later","match":"any","action":"block","session_block_enabled":true,"session_block_ttl_seconds":60,"conditions":[{"question":"cyber_abuse","threshold":0.5}]}]}`), &p)
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": .9})}
	s, st, _ := makeServer(t, p, c)
	w := httptest.NewRecorder()
	r := authorizedRequest("POST", "/v1/responses", strings.NewReader(`{"input":"observe"}`))
	r.Header.Set("Session-Id", "observe-session")
	s.ServeHTTP(w, r)
	if w.Code != 201 || len(st.events) != 1 || len(st.counts[0].SceneMatches) != 1 || st.counts[0].SceneMatches[0].Action != "allow" || len(st.blocked) != 0 {
		t.Fatalf("record-only review: status=%d events=%v counts=%v frozen=%v", w.Code, st.events, st.counts, st.blocked)
	}
}

func TestBlockingRecordOnlyWaitsForClassifier(t *testing.T) {
	p := activePolicy()
	p.Scenes[0].ReviewMode = policy.Blocking
	p.Scenes[0].Action = policy.Allow
	p.Scenes[0].Conditions[0].RecordOnly = true
	c := &parallelClassifier{arrived: make(chan struct{}, 1), release: make(chan struct{})}
	s, st, _ := makeServer(t, p, &testClassifier{})
	s.Classifier = c
	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, authorizedRequest("POST", "/v1/responses", strings.NewReader(`{"input":"wait and record"}`)))
		done <- w.Code
	}()
	select {
	case <-c.arrived:
	case <-time.After(time.Second):
		close(c.release)
		t.Fatal("review did not start")
	}
	select {
	case <-done:
		close(c.release)
		t.Fatal("blocking record-only scene forwarded before review")
	case <-time.After(50 * time.Millisecond):
	}
	close(c.release)
	select {
	case status := <-done:
		if status != 201 {
			t.Fatalf("record-only status %d", status)
		}
	case <-time.After(time.Second):
		t.Fatal("review did not finish")
	}
	if st.events[0].ExecutionMode != policy.Blocking || st.events[0].Decision.Reason != "condition_record_only" {
		t.Fatalf("wrong review mode: %+v", st.events[0])
	}
}

func TestConditionCacheRequiresEveryApplicableFactAndIgnoresV1(t *testing.T) {
	p := activePolicy()
	p.Scenes[0].Conditions = append(p.Scenes[0].Conditions, policy.Condition{Question: "illicit", Threshold: .5})
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": .9})}
	s, _, _ := makeServer(t, p, c)
	cache := &memoryReviewCache{values: map[string]bool{"sael:review:v1:obsolete": false}}
	s.ReviewCache = cache
	send := func() int {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, authorizedRequest("POST", "/v1/responses", strings.NewReader(`{"input":"same"}`)))
		return w.Code
	}
	if send() != 403 || c.calls != 1 {
		t.Fatal("legacy predicate bypassed review")
	}
	for key, value := range cache.values {
		if strings.HasPrefix(key, "sael:review:v2:") && !value {
			delete(cache.values, key)
		}
	}
	c.answers = fullAnswers(nil)
	if send() != 201 || c.calls != 2 {
		t.Fatal("partial cache reused a stale rejection")
	}
}

type memoryReviewCache struct {
	values map[string]bool
	err    error
}

func (c *memoryReviewCache) Lookup(_ context.Context, keys []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, k := range keys {
		if v, ok := c.values[k]; ok {
			out[k] = v
		}
	}
	return out, c.err
}
func (c *memoryReviewCache) Save(_ context.Context, values map[string]bool) error {
	for k, v := range values {
		c.values[k] = v
	}
	return c.err
}

func TestReviewCacheUsesCurrentRulesAndCurrentRequestMetadata(t *testing.T) {
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": .9})}
	p := activePolicy()
	p.Scenes = append(p.Scenes, policy.Scene{ID: "allow", Name: "observe", Match: policy.Any, Action: policy.Allow, Conditions: []policy.Condition{{Question: "cyber_abuse", Threshold: .7}}})
	s, st, forwarded := makeServer(t, p, c)
	cache := &memoryReviewCache{values: map[string]bool{}}
	s.ReviewCache = cache
	send := func(ip string) int {
		r := authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"same input"}]}`))
		r.RemoteAddr = ip + ":1234"
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w.Code
	}
	if send("192.0.2.1") != 403 || send("192.0.2.2") != 403 {
		t.Fatal("repeated hit was not blocked")
	}
	if c.calls != 1 || len(st.events) != 2 || st.events[1].ReviewSource != "cache" || st.events[1].ClientIP != "192.0.2.2" || len(st.events[1].Scores) != 0 || st.counts[1].ClassifierSample {
		t.Fatalf("cache lost request identity or fabricated classifier sample: calls=%d events=%+v", c.calls, st.events)
	}
	st.policy.Scenes[0], st.policy.Scenes[1] = st.policy.Scenes[1], st.policy.Scenes[0]
	if send("192.0.2.3") != 201 || c.calls != 1 || *forwarded != 1 || st.events[2].Decision.SceneID != "allow" {
		t.Fatal("cached priority or action overrode current policy")
	}
	st.policy.Scenes[0].Conditions[0].Threshold = .95
	if send("192.0.2.4") != 403 || c.calls != 2 {
		t.Fatal("condition change did not trigger fresh judgement")
	}
	cache.err = errors.New("cache down")
	if send("192.0.2.5") != 403 || c.calls != 3 {
		t.Fatal("cache failure bypassed review")
	}
}

func TestReviewCacheDoesNotStoreCleanPrompts(t *testing.T) {
	c := &testClassifier{answers: fullAnswers(nil)}
	s, st, _ := makeServer(t, activePolicy(), c)
	cache := &memoryReviewCache{values: map[string]bool{}}
	s.ReviewCache = cache
	for range 2 {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"ordinary"}]}`)))
	}
	if len(cache.values) != 0 || len(st.events) != 0 || c.calls != 2 {
		t.Fatal("clean prompts became stored records/cache")
	}
}

func TestCachedHitFreezesOnlyRejectedSessions(t *testing.T) {
	for _, action := range []policy.Action{policy.Block, policy.Allow} {
		t.Run(string(action), func(t *testing.T) {
			p := activePolicy()
			p.Scenes[0].Action = action
			p.Scenes[0].SessionBlockEnabled, p.Scenes[0].SessionBlockTTLSeconds = true, 60
			c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": .9})}
			s, st, upstream := makeServer(t, p, c)
			s.ReviewCache = &memoryReviewCache{values: map[string]bool{}}
			send := func(session, agent, text string) int {
				r := authorizedRequest("POST", "/v1/responses", strings.NewReader(`{"input":"`+text+`"}`))
				r.Header.Set("Session-Id", session)
				r.Header.Set("User-Agent", agent)
				w := httptest.NewRecorder()
				s.ServeHTTP(w, r)
				s.reviewWG.Wait()
				return w.Code
			}
			want := http.StatusForbidden
			if action == policy.Allow {
				want = http.StatusCreated
			}
			if send("original", "first/1.0", "repeated danger") != want || send("cached", "second/1.0", "repeated danger") != want {
				t.Fatal("initial and cached decisions differ")
			}
			if c.calls != 1 || len(st.events) != 2 || st.events[1].ReviewSource != "cache" || st.events[1].SessionID != "cached" {
				t.Fatalf("cached decision lost current session: calls=%d events=%+v", c.calls, st.events)
			}
			forwarded := *upstream
			if action == policy.Allow {
				if send("cached", "second/1.0", "safe follow-up") != http.StatusCreated || c.calls != 2 || *upstream != forwarded+1 || len(st.blocked) != 0 {
					t.Fatal("record-only cached hit froze the current session")
				}
				return
			}
			if send("cached", "second/1.0", "safe follow-up") != http.StatusForbidden || c.calls != 1 || *upstream != forwarded {
				t.Fatal("cached hit did not freeze the current session's follow-up")
			}
		})
	}
}

func TestReviewCacheStoresFalsePredicatesAlongsideAHit(t *testing.T) {
	p := activePolicy()
	p.Scenes = append(p.Scenes, policy.Scene{ID: "unhit", Name: "unhit", Match: policy.Any, Action: policy.Allow, Conditions: []policy.Condition{{Question: "illicit", Threshold: .5}}})
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": .9})}
	s, st, _ := makeServer(t, p, c)
	cache := &memoryReviewCache{values: map[string]bool{}}
	s.ReviewCache = cache
	for range 2 {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"mixed"}]}`)))
		if w.Code != http.StatusForbidden {
			t.Fatalf("unexpected status %d", w.Code)
		}
	}
	if c.calls != 1 || len(st.events) != 2 || st.events[1].ReviewSource != "cache" {
		t.Fatalf("false scene predicate was not reused: calls=%d events=%+v cache=%v", c.calls, st.events, cache.values)
	}
}

func TestConditionCacheHashIgnoresDisposition(t *testing.T) {
	condition := policy.Condition{Question: "gore", Threshold: 1.5}
	original := conditionCacheKey(JevConfig{}, condition, "raw secret")
	condition.RecordOnly = true
	if conditionCacheKey(JevConfig{}, condition, "raw secret") != original {
		t.Fatal("disposition changed predicate hash")
	}
	if conditionCacheKey(JevConfig{}, condition, "other secret") == original || conditionCacheKey(JevConfig{Model: "new"}, condition, "raw secret") == original {
		t.Fatal("different prompt/classifier shared cache")
	}
	condition.Threshold = 2
	if conditionCacheKey(JevConfig{}, condition, "raw secret") == original {
		t.Fatal("threshold change reused predicate")
	}
}

type waitingReviewCache struct {
	memoryReviewCache
	entered, release chan struct{}
}

func (c *waitingReviewCache) Lookup(ctx context.Context, keys []string) (map[string]bool, error) {
	close(c.entered)
	select {
	case <-c.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return c.memoryReviewCache.Lookup(ctx, keys)
}
func TestNonblockingReviewDoesNotWaitForCache(t *testing.T) {
	p := activePolicy()
	p.Scenes[0].Action = policy.Allow
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": .9})}
	s, st, _ := makeServer(t, p, c)
	cache := &waitingReviewCache{memoryReviewCache: memoryReviewCache{values: map[string]bool{}}, entered: make(chan struct{}), release: make(chan struct{})}
	s.ReviewCache = cache
	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, authorizedRequest("POST", "/v1/responses", strings.NewReader(`{"input":"prompt"}`)))
		done <- w.Code
	}()
	select {
	case status := <-done:
		if status != 201 {
			t.Errorf("forward status %d", status)
		}
	case <-time.After(time.Second):
		t.Error("forwarding waited for the cache")
	}
	close(cache.release)
	s.Close()
	if len(st.events) != 1 || st.events[0].Decision.Action != policy.Allow {
		t.Fatal("background review did not finish")
	}
}
