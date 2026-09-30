package gateway

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestRelayForwardsOnlyVerifiedClientContext(t *testing.T) {
	for _, tc := range []struct {
		name      string
		remote    string
		trusted   bool
		incoming  http.Header
		wantIP    string
		wantProto string
		wantHost  string
	}{
		{
			name: "trusted reverse proxy", remote: "10.0.0.2:4123", trusted: true,
			incoming: http.Header{
				"X-Forwarded-For":   {"198.51.100.9, 203.0.113.42"},
				"X-Forwarded-Proto": {"https"},
				"X-Forwarded-Host":  {"api.example.test"},
				"X-Real-Ip":         {"8.8.8.8"},
			},
			wantIP: "203.0.113.42", wantProto: "https", wantHost: "api.example.test",
		},
		{
			name: "direct client cannot spoof forwarding headers", remote: "198.51.100.7:4123",
			incoming: http.Header{
				"X-Forwarded-For":   {"8.8.8.8"},
				"X-Forwarded-Proto": {"https"},
				"X-Forwarded-Host":  {"attacker.example"},
				"X-Real-Ip":         {"8.8.8.8"},
			},
			wantIP: "198.51.100.7", wantProto: "http", wantHost: "gateway.local:8081",
		},
		{
			name: "trusted proxy without XFF does not trust client X-Real-IP", remote: "10.0.0.2:4123", trusted: true,
			incoming: http.Header{"X-Real-Ip": {"8.8.8.8"}},
			wantIP:   "10.0.0.2", wantProto: "http", wantHost: "gateway.local:8081",
		},
		{
			name: "malformed forwarded host is discarded", remote: "10.0.0.2:4123", trusted: true,
			incoming: http.Header{"X-Forwarded-Host": {"api.example.test#fragment"}},
			wantIP:   "10.0.0.2", wantProto: "http", wantHost: "gateway.local:8081",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var received http.Header
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received = r.Header.Clone()
				w.WriteHeader(http.StatusNoContent)
			}))
			defer upstream.Close()
			store := &testStore{upstream: UpstreamConfig{BaseURL: upstream.URL}}
			s := New(store, nil, "test")
			defer s.Close()
			if tc.trusted {
				s.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("10.0.0.2/32")}
			}
			r := httptest.NewRequest(http.MethodGet, "/v1/models", http.NoBody)
			r.RemoteAddr = tc.remote
			r.Host = "gateway.local:8081"
			r.Header = tc.incoming.Clone()
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != http.StatusNoContent {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			for header, want := range map[string]string{
				"X-Forwarded-For": tc.wantIP, "X-Real-IP": tc.wantIP,
				"X-Forwarded-Proto": tc.wantProto, "X-Forwarded-Host": tc.wantHost,
			} {
				if got := received.Get(header); got != want {
					t.Errorf("%s=%q, want %q", header, got, want)
				}
			}
		})
	}
}
