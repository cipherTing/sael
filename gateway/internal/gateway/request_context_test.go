package gateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/cipherTing/sael/gateway/internal/policy"
)

func TestModelScopeMatchesProductionAndDraft(t *testing.T) {
	var p policy.Policy
	if err := json.Unmarshal([]byte(`{"enabled":true,"scenes":[{"id":"scoped","name":"范围","models":["target","other"],"endpoints":["openai_responses"],"conditions":[{"question":"gore","threshold":1.5}],"match":"all","action":"block"}]}`), &p); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		model  string
		status int
		trace  string
	}{{"target", 403, "effective"}, {"other", 403, "effective"}, {"TARGET", 201, "model_skipped"}, {"unknown", 201, "model_skipped"}, {"", 201, "model_skipped"}} {
		t.Run(tc.model, func(t *testing.T) {
			c := &testClassifier{answers: fullAnswers(map[string]float64{"gore": 2})}
			s, store, _ := makeServer(t, p, c)
			raw, _ := json.Marshal(map[string]any{"input": "hello", "model": tc.model})
			w := httptest.NewRecorder()
			s.ServeHTTP(w, authorizedRequest("POST", "/v1/responses", strings.NewReader(string(raw))))
			if w.Code != tc.status {
				t.Fatalf("model %q: got %d want %d", tc.model, w.Code, tc.status)
			}
			if tc.status == 201 && (len(store.events) != 0 || c.calls != 0) {
				t.Fatal("out-of-scope model must not enter review or create an event")
			}
			raw, _ = json.Marshal(map[string]any{"endpoint": "openai_responses", "model": tc.model, "scores": fullAnswers(map[string]float64{"gore": 2})})
			w = adminRequest(t, s, "POST", "/admin/policy/test", string(raw))
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"`+tc.trace+`"`) {
				t.Fatalf("draft mismatch: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

type capturingClassifier struct{ text string }

func (c *capturingClassifier) Check(_ context.Context, text string) ([]policy.Answer, error) {
	c.text = text
	return fullAnswers(map[string]float64{"cyber_abuse": 0.9}), nil
}

func TestEventContextIsRedactedWithoutChangingForwardedRequest(t *testing.T) {
	p := activePolicy()
	p.Scenes[0].Action = policy.Allow
	s, store, _ := makeServer(t, p, &testClassifier{})
	c := &capturingClassifier{}
	s.Classifier = c
	raw := `{"model":"target","input":"联系 user@example.com；password=private-pass api_key=private-key Bearer private-bearer","stream":true,"reasoning":{"effort":"high"},"service_tier":"priority","max_output_tokens":2048,"previous_response_id":"resp_123","conversation":"conv_456","metadata":{"password":"do-not-store"}}`
	r := authorizedRequest("POST", "/v1/responses", strings.NewReader(raw))
	r.RemoteAddr = "203.0.113.8:4123"
	r.Header.Set("Session_id", "session-123")
	r.Header.Set("User-Agent", "test-client/1")
	r.Header.Set("X-Request-ID", "client-request-1")
	r.Header.Set("Authorization", "Bearer private-auth")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	s.reviewWG.Wait()
	if w.Code != 201 || w.Header().Get("X-Upstream-Body") != raw || !strings.Contains(c.text, "private-pass") {
		t.Fatal("classification or forwarding was altered")
	}
	if len(store.events) != 1 {
		t.Fatalf("events: %d", len(store.events))
	}
	encoded, _ := json.Marshal(store.events[0])
	value := string(encoded)
	for _, secret := range []string{"private-pass", "private-key", "private-bearer", "private-auth", "user@example.com", "do-not-store"} {
		if strings.Contains(value, secret) {
			t.Errorf("stored secret %q", secret)
		}
	}
	for _, want := range []string{`"client_ip":"203.0.113.8"`, `"session_id":"session-123"`, `"user_agent":"test-client/1"`, `"client_request_id":"client-request-1"`, `"reasoning_effort":"high"`, `"service_tier":"priority"`, `"max_output_tokens":2048`, `"previous_response_id":"resp_123"`, `"conversation_id":"conv_456"`} {
		if !strings.Contains(value, want) {
			t.Errorf("missing %s", want)
		}
	}
}

func TestClientIPOnlyTrustsConfiguredProxyChain(t *testing.T) {
	for _, tc := range []struct {
		remote, forwarded, want string
		trust                   bool
	}{
		{"198.51.100.2:1234", "1.2.3.4", "198.51.100.2", false},
		{"10.0.0.2:1234", "1.2.3.4, 203.0.113.7, 10.0.0.3", "203.0.113.7", true},
		{"10.0.0.2:1234", "garbage", "10.0.0.2", true},
	} {
		t.Run(tc.want, func(t *testing.T) {
			s, store, _ := makeServer(t, activePolicy(), &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": 0.9})})
			if tc.trust {
				s.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
			}
			r := authorizedRequest("POST", "/v1/responses", strings.NewReader(`{"input":"hello"}`))
			r.RemoteAddr = tc.remote
			r.Header.Set("X-Forwarded-For", tc.forwarded)
			s.ServeHTTP(httptest.NewRecorder(), r)
			if store.events[0].ClientIP != tc.want {
				t.Fatalf("got %s want %s", store.events[0].ClientIP, tc.want)
			}
		})
	}
}

func TestAnthropicSessionMetadataIsRecordedWithoutUserIdentity(t *testing.T) {
	for _, tc := range []struct{ userID, header, want string }{
		{`{"session_id":"claude-session-1","account_uuid":"private-account","device_id":"private-device"}`, "", "claude-session-1"},
		{`user_private-device_account_private-account_session_123e4567-e89b-12d3-a456-426614174000`, "", "123e4567-e89b-12d3-a456-426614174000"},
		{`{"session_id":"body-session"}`, "header-session", "header-session"},
		{"user@example.com", "", ""},
	} {
		t.Run(tc.want, func(t *testing.T) {
			s, store, _ := makeServer(t, activePolicy(), &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": 0.9})})
			raw, _ := json.Marshal(map[string]any{"metadata": map[string]string{"user_id": tc.userID}, "messages": []map[string]string{{"role": "user", "content": "hello"}}})
			r := authorizedRequest("POST", "/v1/messages", strings.NewReader(string(raw)))
			r.Header.Set("X-Session-Id", tc.header)
			s.ServeHTTP(httptest.NewRecorder(), r)
			if store.events[0].SessionID != tc.want {
				t.Fatalf("session got %q want %q", store.events[0].SessionID, tc.want)
			}
			event, _ := json.Marshal(store.events[0])
			for _, private := range []string{"private-account", "private-device", "user@example.com"} {
				if strings.Contains(string(event), private) {
					t.Fatal("stored unrelated user identity")
				}
			}
		})
	}
}
