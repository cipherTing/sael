package version

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckFiltersNamespacesAndComparesSemanticVersions(t *testing.T) {
	for _, test := range []struct{ current, latest string }{
		{"0.9.0", "0.10.0"}, {"0.11.0-rc.1", "0.11.0-rc.2"},
	} {
		t.Run(test.current, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprint(w, `[
					{"tag_name":"cli/v99.0.0"}, {"tag_name":"sdk/v99.0.0"},
					{"tag_name":"gateway/v0.9.0"}, {"tag_name":"gateway/v0.10.0"},
					{"tag_name":"gateway/v0.11.0-rc.2","prerelease":true},
					{"tag_name":"gateway/v99.0.0","draft":true},
					{"tag_name":"gateway/vinvalid"}]`)
			}))
			defer server.Close()
			checker := New(test.current, "abc123")
			checker.releasesURL = server.URL
			result := checker.Check(context.Background(), false)
			if result.LatestVersion != test.latest || result.UpdateAvailable == nil || !*result.UpdateAvailable || result.CheckError != "" {
				t.Fatalf("unexpected update result: %+v", result)
			}
			if !strings.HasPrefix(result.ReleaseURL, "https://github.com/cipherTing/sael/releases/tag/gateway/v") {
				t.Fatalf("invalid release link: %s", result.ReleaseURL)
			}
		})
	}
}

func TestCheckPaginatesAndCachesSuccessAndFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("page") == "1" {
			_, _ = fmt.Fprint(w, "["+strings.Repeat(`{"tag_name":"cli/v1.0.0"},`, 99)+`{"tag_name":"cli/v1.0.0"}]`)
		} else {
			_, _ = fmt.Fprint(w, `[{"tag_name":"gateway/v1.0.0"}]`)
		}
	}))
	defer server.Close()
	checker := New("1.0.0", "abc123")
	checker.releasesURL = server.URL
	for range 2 {
		result := checker.Check(context.Background(), false)
		if result.LatestVersion != "1.0.0" || result.UpdateAvailable == nil || *result.UpdateAvailable {
			t.Fatalf("unexpected result: %+v", result)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("cached check made %d requests, want two pages", calls.Load())
	}
	checker.checkedAt = time.Now().Add(-time.Hour)
	checker.Check(context.Background(), true)
	if calls.Load() != 4 {
		t.Fatal("manual check did not refresh")
	}

	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer failed.Close()
	checker.releasesURL = failed.URL
	checker.checkedAt = time.Now().Add(-24 * time.Hour)
	for range 2 {
		result := checker.Check(context.Background(), false)
		if result.CheckError == "" || result.UpdateAvailable != nil {
			t.Fatalf("failure presented as up to date: %+v", result)
		}
	}
	if calls.Load() != 5 {
		t.Fatal("failed checks were not cached")
	}
}

func TestDevelopmentBuildDoesNotClaimToBeUpToDate(t *testing.T) {
	checker := New("devel", "")
	result := checker.Check(context.Background(), false)
	if result.CurrentVersion != "devel" || result.UpdateAvailable != nil || result.CheckedAt != nil {
		t.Fatalf("development build has a release verdict: %+v", result)
	}
}
