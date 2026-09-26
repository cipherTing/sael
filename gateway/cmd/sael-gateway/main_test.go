package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
