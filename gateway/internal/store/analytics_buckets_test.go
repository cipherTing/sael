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
)

func TestAnalyticsBucketsKeepClockBoundariesAndQueryRange(t *testing.T) {
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
	for _, at := range []string{"2026-09-24T10:46:00Z", "2026-09-24T10:50:00Z", "2026-09-24T11:05:00Z", "2026-09-25T10:47:00Z"} {
		instant, _ := time.Parse(time.RFC3339, at)
		if err := s.Increment(ctx, gateway.Count{ID: at, Time: instant, Protocol: "openai_chat", Model: "test", Outcome: "clean"}); err != nil {
			t.Fatal(err)
		}
	}
	server := gateway.New(s, nil, "test-password")
	server.Security = adminTestSecurity{}
	defer server.Close()
	login := httptest.NewRecorder()
	server.AdminHandler().ServeHTTP(login, httptest.NewRequest("POST", "/admin/login", strings.NewReader(`{"password":"test-password"}`)))
	for _, start := range []string{"10:47", "10:48"} {
		r := httptest.NewRequest("GET", "/admin/analytics?start=2026-09-24T"+start+":00Z&end=2026-09-25T10:47:00Z&timezone=Asia%2FShanghai", http.NoBody)
		r.AddCookie(login.Result().Cookies()[0])
		w := httptest.NewRecorder()
		server.AdminHandler().ServeHTTP(w, r)
		var result gateway.Analytics
		if w.Code != 200 {
			t.Fatalf("analytics: %d %s", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.StepSeconds != 3600 || len(result.Traffic) != 2 {
			t.Fatalf("wrong hourly buckets: %+v", result)
		}
		for i, want := range []string{"2026-09-24T10:00:00Z", "2026-09-24T11:00:00Z"} {
			if result.Traffic[i].Time.UTC().Format(time.RFC3339) != want || result.Traffic[i].Count != 1 {
				t.Fatalf("bucket %d: %+v", i, result.Traffic[i])
			}
		}
	}
	for _, tc := range []struct {
		granularity, want string
		step              int64
		count             int64
	}{
		{"5m", "2026-09-24T10:50:00Z", 300, 1},
		{"1d", "2026-09-23T16:00:00Z", 86400, 2},
	} {
		r := httptest.NewRequest("GET", "/admin/analytics?start=2026-09-24T10:47:00Z&end=2026-09-25T10:47:00Z&timezone=Asia%2FShanghai&granularity="+tc.granularity, http.NoBody)
		r.AddCookie(login.Result().Cookies()[0])
		w := httptest.NewRecorder()
		server.AdminHandler().ServeHTTP(w, r)
		var result gateway.Analytics
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || result.StepSeconds != tc.step || len(result.Traffic) == 0 || result.Traffic[0].Time.UTC().Format(time.RFC3339) != tc.want || result.Traffic[0].Count != tc.count {
			t.Fatalf("%s: %d %s", tc.granularity, w.Code, w.Body.String())
		}
	}
}
