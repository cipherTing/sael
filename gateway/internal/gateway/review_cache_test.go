package gateway

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cipherTing/sael/gateway/internal/policy"
)

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

func TestSceneCacheHashIgnoresPresentationAndConditionOrder(t *testing.T) {
	scene := activePolicy().Scenes[0]
	scene.Conditions = append(scene.Conditions, policy.Condition{Question: "gore", Threshold: 1.5})
	original := sceneCacheKey(JevConfig{}, scene, "raw secret")
	scene.ID = "new"
	scene.Name = "renamed"
	scene.Note = "note"
	scene.Action = policy.Allow
	scene.Conditions[0], scene.Conditions[1] = scene.Conditions[1], scene.Conditions[0]
	if sceneCacheKey(JevConfig{}, scene, "raw secret") != original {
		t.Fatal("presentation changed condition hash")
	}
	if sceneCacheKey(JevConfig{}, scene, "other secret") == original || sceneCacheKey(JevConfig{Model: "new"}, scene, "raw secret") == original {
		t.Fatal("different prompt/classifier shared cache")
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
