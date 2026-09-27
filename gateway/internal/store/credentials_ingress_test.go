package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cipherTing/sael/gateway/internal/classifier"
	"github.com/cipherTing/sael/gateway/internal/gateway"
	"github.com/cipherTing/sael/gateway/internal/policy"
	"github.com/cipherTing/sael/gateway/internal/testcli"
)

func TestIngressLearnsEncryptedCredentialAndRecordsEachCachedHit(t *testing.T) {
	p, _ := redisFixture(t)
	ctx := context.Background()
	if err := p.InitCredentials(ctx, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	jev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		answers := map[string]any{}
		for _, q := range policy.Questions {
			if q.Type == "score" {
				answers[q.Key] = map[string]any{"type": "score", "score": 2}
			} else {
				answers[q.Key] = map[string]any{"type": "noul", "noul": .9}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": answers})
	}))
	defer jev.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer test-full-client-key-9876" {
			w.WriteHeader(401)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	_, _ = p.UpdateUpstream(ctx, gateway.UpstreamConfig{BaseURL: upstream.URL})
	_, _ = p.UpdateJev(ctx, gateway.JevConfig{BaseURL: jev.URL, Model: "mock", APIKey: "classifier-only-key"})
	old, _ := p.Policy(ctx)
	_, err := p.UpdatePolicy(ctx, old.Version, policy.Policy{Enabled: true, Scenes: []policy.Scene{{ID: "block", Name: "block", Conditions: []policy.Condition{{Question: "gore", Threshold: 1.5}}, Match: policy.Any, Action: policy.Block}}}, "test")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := OpenRedis(ctx, p, os.Getenv("TEST_REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	cache, err := OpenReviewCache(ctx, p, os.Getenv("TEST_REVIEW_CACHE_URL"), os.Getenv("TEST_REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	process, err := classifier.NewCLI(ctx, testcli.Binary(t))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	api := gateway.New(runtime, process, "secret")
	defer api.Close()
	api.ReviewCache = cache
	send := func(key, session string) int {
		r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"input":"same current prompt","model":"m"}`))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("X-Session-Id", session)
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w.Code
	}
	if send("random-key", "unknown") != 401 || calls.Load() != 0 {
		t.Fatal("unknown key entered review")
	}
	if send("test-full-client-key-9876", "first") != 200 || calls.Load() != 0 {
		t.Fatal("first success was retroactively reviewed")
	}
	if send("test-full-client-key-9876", "second") != 403 || send("test-full-client-key-9876", "third") != 403 || calls.Load() != 1 {
		t.Fatal("cache failed to reuse exact prompt")
	}
	deadline := time.Now().Add(3 * time.Second)
	var events []gateway.Event
	for time.Now().Before(deadline) {
		events, err = p.Events(ctx, gateway.EventFilter{Since: time.Now().Add(-time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		if len(events) == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(events) != 2 || events[0].MaskedKey != "test********9876" || events[0].ReviewSource != "cache" || events[0].CredentialID == "" || len(events[0].Scores) != 0 {
		t.Fatalf("cache event %+v", events)
	}
	list, err := runtime.TrustedCredentials(ctx, 30*24*time.Hour, 0, 50)
	if err != nil || list.Total != 1 || list.Items[0].MaskedKey != "test********9876" {
		t.Fatalf("trusted list %+v %v", list, err)
	}
	var raw string
	if err := p.pool.QueryRow(ctx, "SELECT string_agg(body::text,'') FROM audit_events").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "test-full-client-key") || strings.Contains(raw, "classifier-only-key") {
		t.Fatal("plaintext credential leaked into events")
	}
}
