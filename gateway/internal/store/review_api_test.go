package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/cipherTing/sael/gateway/internal/gateway"
	"github.com/cipherTing/sael/gateway/internal/policy"
)

func TestReviewAPIPersistenceKeysCountsAndSourceIsolation(t *testing.T) {
	p, _ := redisFixture(t)
	ctx := context.Background()
	if _, err := p.pool.Exec(ctx, "TRUNCATE review_api_keys,review_api_counts_minute,review_api_scene_counts_minute,review_api_latency_minute"); err != nil {
		t.Fatal(err)
	}
	key, err := p.CreateReviewAPIKey(ctx, "integration", "note")
	if err != nil || key.Secret == "" {
		t.Fatalf("create=%+v %v", key, err)
	}
	var hash []byte
	if err := p.pool.QueryRow(ctx, "SELECT secret_hash FROM review_api_keys WHERE id=$1", key.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte(key.Secret))
	if string(hash) != string(want[:]) {
		t.Fatal("key must persist only its hash")
	}
	if got, err := p.AuthenticateReviewAPIKey(ctx, key.Secret); err != nil || got.ID != key.ID || got.Secret != "" {
		t.Fatalf("auth=%+v %v", got, err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	stats := []gateway.ReviewAPIStat{
		{Time: now.Add(-4 * time.Minute), APIKeyID: key.ID, Outcome: "allowed", DurationMS: 10},
		{Time: now.Add(-3 * time.Minute), APIKeyID: key.ID, Outcome: "hit", DurationMS: 50, SceneID: "one", SceneName: "one"},
		{Time: now.Add(-2 * time.Minute), APIKeyID: key.ID, Outcome: "blocked", DurationMS: 1000, CacheHit: true, SceneID: "one", SceneName: "one"},
		{Time: now.Add(-5 * time.Second), Outcome: "error", DurationMS: 25},
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = writeAggregate(ctx, tx, newAggregate(), nil, stats); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	out, err := p.ReviewAPIOverview(ctx, now.Add(-10*time.Minute), now.Add(time.Minute))
	if err != nil || out.Requests != 4 || out.Allowed != 1 || out.Hits != 2 || out.Blocked != 1 || out.Errors != 1 || out.RPM != 1 || out.P50MS != 25 || out.P95MS != 1000 || len(out.Trend) != 4 {
		t.Fatalf("overview=%+v error=%v", out, err)
	}
	for _, point := range out.Trend {
		if point.Requests != 1 {
			t.Fatalf("trend omitted clean requests: %+v", point)
		}
	}
	keys, err := p.ListReviewAPIKeys(ctx)
	if err != nil || len(keys) != 1 || keys[0].RequestCount != 3 || keys[0].Secret != "" {
		t.Fatalf("list=%+v %v", keys, err)
	}
	for _, source := range []string{"gateway", "review_api"} {
		e := gateway.Event{ID: "source-" + source, Time: now, Kind: "hit", RequestSource: source, Protocol: "openai_responses", ClientIP: "192.0.2.1", Decision: policy.Decision{Action: policy.Block, SceneID: source, SceneName: source}}
		if source == "gateway" {
			e.RequestSource = ""
		}
		if err := p.WriteEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
		list, err := p.Events(ctx, gateway.EventFilter{Since: now.Add(-time.Minute), RequestSource: source})
		if err != nil || len(list) != 1 || list[0].RequestSource != source {
			t.Fatalf("%s events=%+v %v", source, list, err)
		}
	}
	gw, err := p.Overview(ctx, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil || gw.Total != 0 || len(gw.Scenes) != 1 || gw.Scenes[0].Name != "gateway" {
		t.Fatalf("gateway contaminated=%+v %v", gw, err)
	}
	risk, err := p.RiskSources(ctx, gateway.AnalyticsFilter{Since: now.Add(-time.Minute), Until: now.Add(time.Minute)})
	if err != nil || len(risk.IPs) != 1 || risk.IPs[0].Count != 1 {
		t.Fatalf("gateway ranking contaminated=%+v %v", risk, err)
	}
	if err := p.RevokeReviewAPIKey(ctx, key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.AuthenticateReviewAPIKey(ctx, key.Secret); !errors.Is(err, gateway.ErrInvalidReviewAPIKey) {
		t.Fatalf("revoked auth=%v", err)
	}
}

func TestReviewAPIStreamReplayDoesNotDuplicateIndependentCounts(t *testing.T) {
	p, _ := redisFixture(t)
	ctx := context.Background()
	if _, err := p.pool.Exec(ctx, "TRUNCATE review_api_counts_minute,review_api_scene_counts_minute,review_api_latency_minute"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	b := ingestBatch{ReviewStats: []gateway.ReviewAPIStat{{Time: now, Outcome: "allowed", DurationMS: 20}}}
	raw, err := marshalPostgresJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	messages := []redis.XMessage{{ID: "1-0", Values: map[string]any{"data": string(raw)}}}
	for range 2 {
		if err := p.applyStream(ctx, messages); err != nil {
			t.Fatal(err)
		}
	}
	out, err := p.ReviewAPIOverview(ctx, now.Truncate(time.Minute), now.Add(time.Minute))
	if err != nil || out.Requests != 1 {
		t.Fatalf("duplicate replay=%+v %v", out, err)
	}
}
