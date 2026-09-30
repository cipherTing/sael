package store

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cipherTing/sael/gateway/internal/gateway"
	"github.com/cipherTing/sael/gateway/internal/policy"
)

func TestAnalyticsKeepsOnlyHitScoresAndFiltersEndpoints(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn, filepath.Join(t.TempDir(), "spool"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.pool.Exec(ctx, "TRUNCATE gateway_counts_minute, audit_events, gateway_measurements_minute, gateway_scene_matches_minute, gateway_jev_errors_minute")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	for _, c := range []gateway.Count{
		{ID: "analytics-clean", Time: now, Protocol: "openai_chat", Model: "a", Outcome: "clean", ClassifierSample: true, ClassifierMS: 20, JevMS: 10, Scores: []policy.Answer{{Question: "gore", Type: "score", Value: 0.2}}},
		{ID: "analytics-hit", Time: now, Protocol: "openai_chat", Model: "a", Outcome: "blocked", ClassifierSample: true, ClassifierMS: 50, JevMS: 20, Scores: []policy.Answer{{Question: "gore", Type: "score", Value: 2}}, SceneMatches: []gateway.SceneMatch{{SceneID: "first", Name: "First", Action: "block", WinnerID: "first", WinnerName: "First"}, {SceneID: "second", Name: "Second", Action: "allow", WinnerID: "first", WinnerName: "First"}}},
		{ID: "analytics-other", Time: now, Protocol: "anthropic", Model: "b", Outcome: "unreviewed", ClassifierSample: true, ClassifierMS: 5000, ErrorKind: "classifier_timeout"},
	} {
		if err := s.Increment(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	server := gateway.New(s, nil, "test-password")
	server.Security = &adminTestSecurity{}
	defer server.Close()
	login := httptest.NewRecorder()
	server.AdminHandler().ServeHTTP(login, httptest.NewRequest("POST", "/admin/login", strings.NewReader(`{"password":"test-password"}`)))
	r := httptest.NewRequest("GET", "/admin/analytics?start=2026-09-24T10:00:00Z&end=2026-09-24T10:01:00Z&endpoint=openai_chat&model=a", http.NoBody)
	r.AddCookie(login.Result().Cookies()[0])
	w := httptest.NewRecorder()
	server.AdminHandler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("analytics: %d %s", w.Code, w.Body.String())
	}
	var result gateway.Analytics
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	var requests, scores, reviewSamples, matched, effective int64
	for _, p := range result.Traffic {
		requests += p.Count
		if p.Endpoint != "openai_chat" || p.Model != "a" {
			t.Fatal("filter leaked")
		}
	}
	for _, p := range result.Distributions {
		if p.Model != "a" {
			t.Fatalf("distribution lost model dimension: %+v", p)
		}
		if p.Metric == "hit_score:gore" {
			scores += p.Count
		}
		if p.Metric == "review_ms" {
			reviewSamples += p.Count
		}
	}
	for _, p := range result.Scenes {
		matched += p.Count
		if p.SceneID == p.WinnerID {
			effective += p.Count
		}
	}
	if requests != 2 || scores != 1 || reviewSamples != 2 || matched != 2 || effective != 1 || len(result.Errors) != 0 {
		t.Fatalf("unexpected aggregation: %s", w.Body.String())
	}
	var events int
	_ = s.pool.QueryRow(ctx, "SELECT count(*) FROM audit_events").Scan(&events)
	if events != 0 {
		t.Fatal("aggregation stored request events")
	}
}

func TestAnalyticsTrafficReturnsClassifierSumForRealSamplesOnly(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn, filepath.Join(t.TempDir(), "spool"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.pool.Exec(ctx, "TRUNCATE gateway_counts_minute, gateway_measurements_minute, gateway_scene_matches_minute, gateway_jev_errors_minute"); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	for _, count := range []gateway.Count{
		{ID: "latency-sample-20", Time: now, Protocol: "openai_chat", Model: "latency", Outcome: "clean", ClassifierSample: true, ClassifierMS: 20},
		{ID: "latency-sample-50", Time: now, Protocol: "openai_chat", Model: "latency", Outcome: "hit_allowed", ClassifierSample: true, ClassifierMS: 50},
		{ID: "latency-cache-hit", Time: now, Protocol: "openai_chat", Model: "latency", Outcome: "hit_allowed", CacheLookup: true, CacheHit: true, ClassifierMS: 5000},
		{ID: "latency-other-endpoint", Time: now, Protocol: "anthropic", Model: "latency", Outcome: "clean", ClassifierSample: true, ClassifierMS: 9000},
		{ID: "latency-other-model", Time: now, Protocol: "openai_chat", Model: "other", Outcome: "clean", ClassifierSample: true, ClassifierMS: 9000},
		{ID: "latency-after-window", Time: now.Add(time.Minute), Protocol: "openai_chat", Model: "latency", Outcome: "clean", ClassifierSample: true, ClassifierMS: 9000},
	} {
		if err := s.Increment(ctx, count); err != nil {
			t.Fatal(err)
		}
	}
	result, err := s.Analytics(ctx, gateway.AnalyticsFilter{Since: now, Until: now.Add(time.Minute), Endpoint: "openai_chat", Model: "latency", Granularity: "1m", Timezone: "UTC"})
	if err != nil || len(result.Traffic) != 2 {
		t.Fatalf("traffic: %v %+v", err, result.Traffic)
	}
	raw, _ := json.Marshal(result.Traffic)
	var points []struct {
		Sum   int64 `json:"classifier_sum_ms"`
		Calls int64 `json:"classifier_calls"`
		Count int64 `json:"count"`
	}
	if err := json.Unmarshal(raw, &points); err != nil {
		t.Fatal(err)
	}
	var sum, calls, total int64
	for _, point := range points {
		sum += point.Sum
		calls += point.Calls
		total += point.Count
	}
	if sum != 70 || calls != 2 || total != 3 {
		t.Fatalf("classifier aggregate sum=%d calls=%d total=%d traffic=%+v", sum, calls, total, result.Traffic)
	}
}

func TestEventsRespectEndTimeEndpointAndScene(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn, filepath.Join(t.TempDir(), "spool"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, _ = s.pool.Exec(ctx, "TRUNCATE audit_events")
	for _, e := range []gateway.Event{
		{ID: "wanted", Time: time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC), Protocol: "openai_chat", Model: "a", Kind: "hit", Decision: policy.Decision{Action: policy.Block, SceneID: "target"}},
		{ID: "wrong-endpoint", Time: time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC), Protocol: "anthropic", Model: "a", Kind: "hit", Decision: policy.Decision{Action: policy.Block, SceneID: "target"}},
		{ID: "wrong-time", Time: time.Date(2026, 9, 24, 11, 0, 0, 0, time.UTC), Protocol: "openai_chat", Model: "a", Kind: "hit", Decision: policy.Decision{Action: policy.Block, SceneID: "target"}},
		{ID: "wrong-scene", Time: time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC), Protocol: "openai_chat", Model: "a", Kind: "hit", Decision: policy.Decision{Action: policy.Block, SceneID: "other"}},
	} {
		if err := s.WriteEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	server := gateway.New(s, nil, "test-password")
	server.Security = &adminTestSecurity{}
	defer server.Close()
	login := httptest.NewRecorder()
	server.AdminHandler().ServeHTTP(login, httptest.NewRequest("POST", "/admin/login", strings.NewReader(`{"password":"test-password"}`)))
	r := httptest.NewRequest("GET", "/admin/events?start=2026-09-24T10:00:00Z&end=2026-09-24T10:01:00Z&endpoint=openai_chat&model=a&scene=target", http.NoBody)
	r.AddCookie(login.Result().Cookies()[0])
	w := httptest.NewRecorder()
	server.AdminHandler().ServeHTTP(w, r)
	var rows []gateway.Event
	_ = json.Unmarshal(w.Body.Bytes(), &rows)
	if w.Code != 200 || len(rows) != 1 || rows[0].ID != "wanted" {
		t.Fatalf("event filters: %d %s", w.Code, w.Body.String())
	}
}

func TestStoredPromptsAreRedactedAndRequestContextIsSearchable(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn, filepath.Join(t.TempDir(), "spool"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, _ = s.pool.Exec(ctx, "TRUNCATE audit_events")
	event := gateway.Event{ID: "privacy-test", RequestID: "privacy-request", Time: time.Now().UTC(), Kind: "hit", ClientIP: "203.0.113.7", SessionID: "session-unique", UserAgent: "test-client", Text: `password="never store" user@example.com`, TextPreview: "user@example.com", Decision: policy.Decision{Action: policy.Allow}}
	if err := s.WriteEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := s.pool.QueryRow(ctx, "SELECT body::text FROM audit_events WHERE id=$1", event.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "never store") || strings.Contains(raw, "user@example.com") {
		t.Fatal("database contains unredacted prompt")
	}
	for _, search := range []string{"203.0.113.7", "session-unique"} {
		rows, err := s.Events(ctx, gateway.EventFilter{Since: event.Time.Add(-time.Second), Search: search})
		if err != nil || len(rows) != 1 {
			t.Fatalf("request context search %q: %v %d", search, err, len(rows))
		}
	}
	for _, f := range []gateway.EventFilter{
		{Since: event.Time.Add(-time.Second), ClientIP: "203.0.113.7"},
		{Since: event.Time.Add(-time.Second), SessionID: "session-unique"},
	} {
		rows, err := s.Events(ctx, f)
		if err != nil || len(rows) != 1 {
			t.Fatalf("exact filter: %v %d", err, len(rows))
		}
	}
	rows, err := s.Events(ctx, gateway.EventFilter{Since: event.Time.Add(-time.Second), SessionID: "session"})
	if err != nil || len(rows) != 0 {
		t.Fatalf("session filter must be exact: %v %d", err, len(rows))
	}
}

func TestUpgradeRedactsHistoricalTextWithoutChangingAuditDecision(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.pool.Exec(ctx, `DELETE FROM gateway_data_migrations WHERE name='prompt-redaction-v1'; INSERT INTO audit_events(id,time,kind,action,request_id,body) VALUES ('legacy-private',now(),'hit','block','legacy-request','{"text":"password=old-secret","text_preview":"user@example.com","decision":{"action":"block","scene_id":"old-scene"},"custom_old_field":42}') ON CONFLICT(id) DO UPDATE SET body=EXCLUDED.body`)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.redactHistoricalEvents(ctx); err != nil {
		t.Fatal(err)
	}
	var raw string
	_ = s.pool.QueryRow(ctx, `SELECT body::text FROM audit_events WHERE id='legacy-private'`).Scan(&raw)
	if strings.Contains(raw, "old-secret") || strings.Contains(raw, "user@example.com") || !strings.Contains(raw, "old-scene") || !strings.Contains(raw, "custom_old_field") {
		t.Fatalf("migration lost data or leaked prompt: %s", raw)
	}
	if err = s.redactHistoricalEvents(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestImagesHistoryGroupsBothOperationsAndFiltersRecords(t *testing.T) {
	p, _ := redisFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Minute)
	for i, protocol := range []string{"openai_images_generations", "openai_images_edits", "openai_images_variations"} {
		if err := p.increment(ctx, gateway.Count{Time: now, Protocol: protocol, Model: "image", Outcome: "blocked"}); err != nil {
			t.Fatal(err)
		}
		if err := p.WriteEvent(ctx, gateway.Event{ID: protocol, RequestID: protocol, Time: now, Protocol: protocol, Kind: "hit", CredentialID: "", Decision: policy.Decision{Action: policy.Block}}); err != nil {
			t.Fatal(i, err)
		}
	}
	a, err := p.Analytics(ctx, gateway.AnalyticsFilter{Since: now, Until: now.Add(time.Minute), Endpoint: "openai_images", Granularity: "1m", Timezone: "UTC"})
	if err != nil || len(a.Traffic) != 1 || a.Traffic[0].Count != 2 || a.Traffic[0].Endpoint != "openai_images" {
		t.Fatalf("images history %+v %v", a.Traffic, err)
	}
	rows, err := p.Events(ctx, gateway.EventFilter{Since: now, Endpoint: "openai_images"})
	if err != nil || len(rows) != 2 {
		t.Fatalf("images records %d %v", len(rows), err)
	}
}
