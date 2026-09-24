package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cipherTing/sael/gateway/internal/gateway"
	"github.com/cipherTing/sael/gateway/internal/policy"
)

func TestPostgresPolicyEventsAndCounts(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn, filepath.Join(t.TempDir(), "spool.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, _ = s.pool.Exec(ctx, "TRUNCATE audit_events, gateway_counts_minute, policy_changes")
	_, _ = s.pool.Exec(ctx, `UPDATE gateway_policy SET version=1, body='{"enabled":false,"version":1,"thresholds":{},"scenes":[],"unmatched_action":""}' WHERE id=1`)
	empty, err := s.Overview(ctx, time.Now().Add(-time.Hour))
	if err != nil || empty.Total != 0 {
		t.Fatalf("empty overview: %+v %v", empty, err)
	}
	p, err := s.Policy(ctx)
	if err != nil || p.Enabled || p.Version != 1 {
		t.Fatalf("initial policy: %+v %v", p, err)
	}
	preview, days := 0, 30
	next := policy.Policy{Version: 1, Thresholds: map[string]float64{"cyber_abuse": 0.8}, Scenes: []policy.Scene{{ID: "scene", Name: "cyber", Questions: []string{"cyber_abuse"}, Match: policy.Any, Action: policy.Block}}, UnmatchedAction: policy.Allow, PreviewChars: &preview, RetentionDays: &days}
	updated, err := s.UpdatePolicy(ctx, 1, next, "admin")
	if err != nil || updated.Version != 2 {
		t.Fatalf("update: %+v %v", updated, err)
	}
	if _, err := s.UpdatePolicy(ctx, 1, next, "admin"); !errors.Is(err, gateway.ErrConflict) {
		t.Fatalf("stale update: %v", err)
	}
	if loaded, err := s.Policy(ctx); err != nil || loaded.Scenes[0].ID != "scene" {
		t.Fatalf("reloaded: %+v %v", loaded, err)
	}
	if changes, err := s.Changes(ctx); err != nil || len(changes) != 1 || changes[0].Version != 2 {
		t.Fatalf("changes: %+v %v", changes, err)
	}
	now := time.Now().UTC()
	event := gateway.Event{ID: "event-1", Time: now, Kind: "hit", RequestID: "request-1", Protocol: "openai_chat", Model: "test", Decision: policy.Decision{Action: policy.Block, SceneID: "scene", SceneName: "cyber", Hits: []policy.Hit{{Question: "cyber_abuse", Value: 0.9, Threshold: 0.8}}}}
	if err := s.WriteEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := s.Increment(ctx, gateway.Count{Time: now, Protocol: "openai_chat", Model: "test", Outcome: "blocked"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Increment(ctx, gateway.Count{Time: now, Protocol: "openai_chat", Model: "test", Outcome: "clean"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Event(ctx, "event-1")
	if err != nil || got.RequestID != "request-1" {
		t.Fatalf("event: %+v %v", got, err)
	}
	items, err := s.Events(ctx, gateway.EventFilter{Since: now.Add(-time.Hour), Kind: "hit", Action: "block", Search: "request-1", Limit: 50})
	if err != nil || len(items) != 1 {
		t.Fatalf("events: %+v %v", items, err)
	}
	overview, err := s.Overview(ctx, now.Add(-time.Hour))
	if err != nil || overview.Total != 2 || overview.Checked != 2 || overview.Hits != 1 || overview.Blocked != 1 || len(overview.Scenes) != 1 || overview.Scenes[0].Name != "cyber" {
		t.Fatalf("overview: %+v %v", overview, err)
	}
}

func TestPostgresPersistsJevConnection(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn, filepath.Join(t.TempDir(), "spool.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	saved, err := s.UpdateJev(ctx, gateway.JevConfig{BaseURL: "https://api.example/v1", Model: "jev-test", APIKey: "saved-key"})
	if err != nil || saved.APIKey != "saved-key" {
		t.Fatalf("save: %+v %v", saved, err)
	}
	loaded, err := s.Jev(ctx)
	if err != nil || loaded.BaseURL != "https://api.example/v1" || loaded.APIKey != "saved-key" {
		t.Fatalf("load: %+v %v", loaded, err)
	}
}

func TestSavedJevConnectionRemainsAvailableDuringDatabaseOutage(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn, filepath.Join(t.TempDir(), "spool.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateJev(ctx, gateway.JevConfig{BaseURL: "https://api.example/v1", Model: "jev-test", APIKey: "saved-key"}); err != nil {
		t.Fatal(err)
	}
	s.pool.Close()
	got, err := s.Jev(ctx)
	if err != nil || got.APIKey != "saved-key" {
		t.Fatalf("cached config: %+v %v", got, err)
	}
}

func TestOverviewAggregatesClassifierLatencyAndUpstreamFailures(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn, filepath.Join(t.TempDir(), "spool.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, _ = s.pool.Exec(ctx, "TRUNCATE gateway_counts_minute")
	now := time.Now().UTC()
	for _, c := range []gateway.Count{
		{ID: "latency-1", Time: now, Protocol: "openai_chat", Outcome: "clean", ClassifierSample: true, ClassifierMS: 20},
		{ID: "latency-2", Time: now, Protocol: "openai_chat", Outcome: "hit_allowed", ClassifierSample: true, ClassifierMS: 40, UpstreamError: true},
	} {
		if err := s.Increment(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Overview(ctx, now.Add(-time.Minute))
	if err != nil || got.Total != 2 || got.UpstreamErrors != 1 || got.ClassifierAvgMS != 30 {
		t.Fatalf("overview: %+v %v", got, err)
	}
}

func TestOverviewIncludesFailuresBeforeReview(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn, filepath.Join(t.TempDir(), "spool.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.pool.Exec(ctx, "TRUNCATE gateway_counts_minute"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, outcome := range []string{"request_error", "gateway_error"} {
		if err := s.Increment(ctx, gateway.Count{ID: outcome, Time: now, Protocol: "openai_chat", Outcome: outcome}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Overview(ctx, now.Add(-time.Minute))
	if err != nil || got.Total != 2 || got.Checked != 0 || got.Hits != 0 || got.Blocked != 0 || len(got.Trend) != 2 {
		t.Fatalf("overview: %+v %v", got, err)
	}
}

func TestFailedDatabaseEventWriteReplaysFromLocalSpool(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	spool := filepath.Join(t.TempDir(), "events.jsonl")
	s, err := Open(ctx, dsn, spool)
	if err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("spooled-%d", time.Now().UnixNano())
	s.Close()
	event := gateway.Event{ID: id, Time: time.Now().UTC(), Kind: "failure", RequestID: id, Decision: policy.Decision{Action: policy.Allow}}
	if err := s.WriteEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(spool); err != nil {
		t.Fatalf("spool missing: %v", err)
	}
	recovered, err := Open(ctx, dsn, spool)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if err := recovered.Replay(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := recovered.Event(ctx, id); err != nil {
		t.Fatalf("event not replayed: %v", err)
	}
	if _, err := os.Stat(spool); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("spool still present: %v", err)
	}
}

func TestPolicySnapshotRemainsAvailableDuringDatabaseOutage(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	s, err := Open(context.Background(), dsn, filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	s.pool.Close()
	p, err := s.Policy(context.Background())
	if err != nil || p.Version < 1 {
		t.Fatalf("cached policy: %+v %v", p, err)
	}
}

func TestFailedCountWriteReplaysExactlyOnce(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	spool := filepath.Join(t.TempDir(), "events.jsonl")
	s, err := Open(ctx, dsn, spool)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.pool.Exec(ctx, "TRUNCATE gateway_counts_minute, replayed_counts")
	s.pool.Close()
	count := gateway.Count{ID: fmt.Sprintf("count-%d", time.Now().UnixNano()), Time: time.Now().UTC(), Protocol: "openai_chat", Model: "test", Outcome: "blocked"}
	if err := s.Increment(ctx, count); err != nil {
		t.Fatal(err)
	}
	spooled, err := os.ReadFile(spool + ".counts")
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := Open(ctx, dsn, spool)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if err := recovered.Replay(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spool+".counts", spooled, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := recovered.Replay(ctx); err != nil {
		t.Fatal(err)
	}
	overview, err := recovered.Overview(ctx, count.Time.Add(-time.Minute))
	if err != nil || overview.Total != 1 || overview.Blocked != 1 {
		t.Fatalf("replayed count: %+v %v", overview, err)
	}
}
