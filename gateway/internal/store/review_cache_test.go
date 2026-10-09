package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cipherTing/sael/gateway/internal/gateway"
	"github.com/cipherTing/sael/gateway/internal/policy"
)

func TestPostgresPersistsConditionDispositionAndCacheAuditFacts(t *testing.T) {
	p, _ := redisFixture(t)
	ctx := context.Background()
	next := policy.Policy{Enabled: true, Scenes: []policy.Scene{{ID: "observe", Name: "observe", Match: policy.Any, ReviewMode: policy.Blocking, Action: policy.Block, Conditions: []policy.Condition{{Question: "gore", Threshold: 1.5, RecordOnly: true}}}}}
	if _, err := p.UpdatePolicy(ctx, gateway.PolicyUpdate{Replace: &next}, "test"); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(ctx, os.Getenv("TEST_DATABASE_URL"), filepath.Join(t.TempDir(), "spool"))
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	loaded, err := restarted.Policy(ctx)
	if err != nil || loaded.Scenes[0].ReviewMode != policy.Blocking || !loaded.Scenes[0].Conditions[0].RecordOnly || loaded.Scenes[0].Action != policy.Allow {
		t.Fatalf("policy roundtrip: %+v %v", loaded, err)
	}
	decision, trace, err := policy.EvaluatePredicates(loaded, map[policy.ConditionKey]bool{{Question: "gore", Threshold: 1.5}: true}, "openai_responses", "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	event := gateway.Event{ID: "cached-condition-facts", Time: now, Kind: "hit", RequestID: "request", Protocol: "openai_responses", ReviewSource: "cache", ExecutionMode: policy.Blocking, Decision: decision, Trace: trace}
	if err := p.WriteEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	items, err := p.Events(ctx, gateway.EventFilter{Since: now.Add(-time.Minute), Limit: 50})
	if err != nil || len(items) != 1 {
		t.Fatalf("list=%+v error=%v", items, err)
	}
	detail, err := p.Event(ctx, event.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range []gateway.Event{items[0], detail} {
		if got.ExecutionMode != policy.Blocking || got.Decision.Reason != "condition_record_only" || len(got.Decision.Hits) != 1 || got.Decision.Hits[0].Value != nil || got.Decision.Hits[0].RecordOnly == nil || !*got.Decision.Hits[0].RecordOnly || got.Trace[0].Conditions[0].Value != nil {
			t.Fatalf("audit projection lost facts: %+v", got)
		}
	}
}

func TestRedisReviewCacheExpirationConfigurationAndReconnect(t *testing.T) {
	p, _ := redisFixture(t)
	ctx := context.Background()
	c, err := OpenReviewCache(ctx, p, os.Getenv("TEST_REVIEW_CACHE_URL"), os.Getenv("TEST_REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r := c.client
	if err := r.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.Configure(ctx, gateway.ReviewCacheConfig{TTLDays: 7, MaxBytes: 16 << 20}); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(ctx, map[string]bool{"sael:review:test:yes": true, "sael:review:test:no": false}); err != nil {
		t.Fatal(err)
	}
	got, err := c.Lookup(ctx, []string{"sael:review:test:yes", "sael:review:test:no", "absent"})
	if err != nil || !got["sael:review:test:yes"] || len(got) != 2 {
		t.Fatalf("verdicts %v %v", got, err)
	}
	before := r.PTTL(ctx, "sael:review:test:yes").Val()
	time.Sleep(5 * time.Millisecond)
	_, _ = c.Lookup(ctx, []string{"sael:review:test:yes"})
	if r.PTTL(ctx, "sael:review:test:yes").Val() >= before {
		t.Fatal("lookup extended TTL")
	}
	_ = r.PExpire(ctx, "sael:review:test:yes", time.Millisecond).Err()
	time.Sleep(5 * time.Millisecond)
	got, err = c.Lookup(ctx, []string{"sael:review:test:yes"})
	if err != nil || len(got) != 0 {
		t.Fatal("expired verdict reused")
	}
	if err := r.Set(ctx, "sael:review:test:invalid", "garbage", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	invalid, err := c.Lookup(ctx, []string{"sael:review:test:invalid"})
	if err != nil || len(invalid) != 0 {
		t.Fatal("corrupt cached verdict was reused")
	}
	status, err := c.Status(ctx)
	if err != nil || !status.Available || status.EffectiveMaxBytes != 16<<20 {
		t.Fatalf("status %+v %v", status, err)
	}
	c.Close()
	restarted, err := OpenReviewCache(ctx, p, os.Getenv("TEST_REVIEW_CACHE_URL"), os.Getenv("TEST_REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	status, err = restarted.Status(ctx)
	if err != nil || status.EffectiveMaxBytes != 16<<20 || status.TTLDays != 7 {
		t.Fatal("saved cache config lost on restart", status, err)
	}
}

func TestReviewCacheCannotChangePrimaryRedisEvictionPolicy(t *testing.T) {
	p, r := redisFixture(t)
	ctx := context.Background()
	before, err := r.ConfigGet(ctx, "maxmemory-policy").Result()
	if err != nil {
		t.Fatal(err)
	}
	c, err := OpenReviewCache(ctx, p, os.Getenv("TEST_REDIS_URL"), os.Getenv("TEST_REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Configure(ctx, gateway.ReviewCacheConfig{TTLDays: 7, MaxBytes: 16 << 20}); err == nil {
		t.Fatal("shared instance accepted as disposable cache")
	}
	after, _ := r.ConfigGet(ctx, "maxmemory-policy").Result()
	if before["maxmemory-policy"] != after["maxmemory-policy"] {
		t.Fatal("primary Redis eviction policy changed")
	}
}

func TestCanceledCacheOperationDoesNotDisableReuseForOtherRequests(t *testing.T) {
	p, _ := redisFixture(t)
	ctx := context.Background()
	c, err := OpenReviewCache(ctx, p, os.Getenv("TEST_REVIEW_CACHE_URL"), os.Getenv("TEST_REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	key := "sael:review:test:request-cancellation"
	if err := c.Save(ctx, map[string]bool{key: true}); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"lookup", "save"} {
		t.Run(operation, func(t *testing.T) {
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			var err error
			if operation == "lookup" {
				_, err = c.Lookup(canceled, []string{key})
			} else {
				err = c.Save(canceled, map[string]bool{key: true})
			}
			if err == nil {
				t.Fatal("canceled operation unexpectedly completed")
			}
			got, err := c.Lookup(ctx, []string{key})
			if err != nil || !got[key] {
				t.Fatalf("one canceled request disabled reuse for another: %v %v", got, err)
			}
		})
	}
}
