package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWebHandlerServesAppRoutesAndKeepsAdminAPI(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("app entry"), 0o600); err != nil {
		t.Fatal(err)
	}
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
	h := webHandler(api, dir)
	for _, path := range []string{"/", "/settings", "/events"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, http.NoBody))
		if w.Code != 200 || w.Body.String() != "app entry" {
			t.Fatalf("%s: %d %q", path, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/admin/policy", http.NoBody))
	if w.Code != 401 {
		t.Fatalf("admin routed to UI: %d", w.Code)
	}
}

func TestCLIExitStopsTheGatewayAndReportsFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	exited := make(chan struct{})
	close(exited)
	start := time.Now()
	err := serve(ctx, []*http.Server{{Addr: "127.0.0.1:0", ReadHeaderTimeout: time.Second}}, exited)
	if err == nil || time.Since(start) > 500*time.Millisecond {
		t.Fatal("gateway ignored the classifier process exit")
	}
}

func TestNormalShutdownDoesNotReportCLIExitAsFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := serve(ctx, nil, make(chan struct{})); err != nil {
		t.Fatal(err)
	}
}

func TestManagementListenerDoesNotForwardAIRequests(t *testing.T) {
	calls := 0
	h := webHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(201) }), t.TempDir())
	for _, method := range []string{"GET", "POST"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, "/v1/responses", http.NoBody))
		if w.Code != 404 || calls != 0 {
			t.Fatalf("management forwarded business traffic: %d %d", w.Code, calls)
		}
	}
}
