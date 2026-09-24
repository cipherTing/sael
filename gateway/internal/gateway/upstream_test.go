package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cipherTing/sael/gateway/internal/policy"
)

func TestAdminChangesForwardingDestinationAndPreservesRequestAuthorization(t *testing.T) {
	s, store, oldCalls := makeServer(t, policy.Policy{Enabled: false}, &testClassifier{})
	newCalls := 0
	newUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		newCalls++
		if r.URL.Path != "/v1/chat/completions" || r.URL.RawQuery != "x=1" || r.Header.Get("Authorization") != "Bearer client-key" {
			t.Errorf("forwarded request: path=%s query=%s auth=%s", r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, "new upstream")
	}))
	defer newUpstream.Close()
	put := adminRequest(t, s, http.MethodPut, "/admin/upstream", `{"base_url":"`+newUpstream.URL+`"}`)
	if put.Code != http.StatusOK || store.upstream.BaseURL != newUpstream.URL {
		t.Fatalf("saved destination: status=%d config=%+v body=%s", put.Code, store.upstream, put.Body.String())
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions?x=1", strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"hello"}]}`))
	r.Header.Set("Authorization", "Bearer client-key")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusOK || *oldCalls != 0 || newCalls != 1 {
		t.Fatalf("forward status=%d old=%d new=%d", w.Code, *oldCalls, newCalls)
	}
	get := adminRequest(t, s, http.MethodGet, "/admin/upstream", "")
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), newUpstream.URL) || strings.Contains(get.Body.String(), "api_key") {
		t.Fatalf("destination config: status=%d body=%s", get.Code, get.Body.String())
	}
}

func TestUpstreamSettingsRejectKeyManagementFields(t *testing.T) {
	s, store, _ := makeServer(t, policy.Policy{Enabled: false}, &testClassifier{})
	before := store.upstream.BaseURL
	w := adminRequest(t, s, http.MethodPut, "/admin/upstream", `{"base_url":"https://other.example","api_key":"unexpected"}`)
	if w.Code != http.StatusBadRequest || store.upstream.BaseURL != before {
		t.Fatalf("unrelated key accepted: status=%d config=%+v", w.Code, store.upstream)
	}
}
