package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cipherTing/sael/gateway/internal/policy"
)

func TestSessionFreezeIsIsolatedByCallerAndExpires(t *testing.T) {
	p := activePolicy()
	p.Scenes[0].SessionBlockEnabled, p.Scenes[0].SessionBlockTTLSeconds = true, 60
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": 0.9})}
	s, store, upstream := makeServer(t, p, c)
	call := func(key, session string) int {
		r := authorizedRequest("POST", "/v1/responses", strings.NewReader("{\"input\":\"hello\"}"))
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		if session != "" {
			r.Header.Set("Session-Id", session)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w.Code
	}
	if call("caller-a", "shared-session") != 403 {
		t.Fatal("initial request was not blocked")
	}
	for key := range store.blocked {
		if strings.Contains(key, "shared-session") || strings.Contains(key, "caller-a") {
			t.Fatal("freeze key leaked credentials or session")
		}
	}
	c.answers = fullAnswers(nil)
	if call("caller-b", "shared-session") != 201 {
		t.Fatal("one caller froze another caller's session")
	}
	before := c.calls
	if call("caller-a", "shared-session") != 403 || c.calls != before {
		t.Fatal("frozen session invoked classifier")
	}
	for key := range store.blocked {
		store.blocked[key] = time.Now().Add(-time.Second)
	}
	if call("caller-a", "shared-session") != 201 {
		t.Fatal("expired freeze did not release session")
	}
	if *upstream != 2 {
		t.Fatalf("unexpected upstream count: %d", *upstream)
	}
}

func TestSessionFreezeNeedsStableSessionAndOnlyBlocksMonitoredRequests(t *testing.T) {
	p := activePolicy()
	p.Scenes[0].SessionBlockEnabled, p.Scenes[0].SessionBlockTTLSeconds = true, 60
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": 0.9})}
	s, store, _ := makeServer(t, p, c)
	r := authorizedRequest("POST", "/v1/responses", strings.NewReader("{\"input\":\"hello\"}"))
	r.Header.Del("Authorization")
	r.Header.Set("Session-Id", "without-credential")
	s.ServeHTTP(httptest.NewRecorder(), r)
	if len(store.blocked) != 0 {
		t.Fatal("anonymous callers must not share a freeze namespace")
	}
	r = authorizedRequest("POST", "/v1/responses", strings.NewReader("{\"input\":\"hello\"}"))
	r.Header.Set("Authorization", "Bearer caller-a")
	s.ServeHTTP(httptest.NewRecorder(), r)
	if len(store.blocked) != 0 {
		t.Fatal("missing session must not freeze caller or IP")
	}
}

func TestSessionFreezeMatchesAHistoryContinuationWithoutExplicitSessionID(t *testing.T) {
	p := activePolicy()
	p.Scenes[0].SessionBlockEnabled, p.Scenes[0].SessionBlockTTLSeconds = true, 60
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": 0.9})}
	s, store, upstream := makeServer(t, p, c)
	first := authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"danger"}]}`))
	first.Header.Set("User-Agent", "client/1.0")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, first)
	if w.Code != http.StatusForbidden || len(store.blocked) < 2 {
		t.Fatalf("initial transcript hit did not create exact and scope blocks: status=%d blocks=%d", w.Code, len(store.blocked))
	}
	checks := c.calls
	c.answers = fullAnswers(nil)
	second := authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"danger"},{"role":"assistant","content":"denied"},{"role":"user","content":"continue"}]}`))
	second.Header.Set("User-Agent", "client/1.9")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, second)
	if w.Code != http.StatusForbidden || c.calls != checks || *upstream != 0 {
		t.Fatalf("history continuation was not frozen: status=%d classifier=%d upstream=%d", w.Code, c.calls, *upstream)
	}
}

func TestSessionFreezeDoesNotBlockASeparateConversationWithSameOpening(t *testing.T) {
	p := activePolicy()
	p.Scenes[0].SessionBlockEnabled, p.Scenes[0].SessionBlockTTLSeconds = true, 60
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": 0.9})}
	s, store, _ := makeServer(t, p, c)
	first := authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"danger"}]}`))
	first.Header.Set("User-Agent", "client/1.0")
	s.ServeHTTP(httptest.NewRecorder(), first)
	c.answers = fullAnswers(nil)
	separate := authorizedRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"different"}]}`))
	separate.Header.Set("User-Agent", "other-client/1.0")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, separate)
	if w.Code != http.StatusCreated || len(store.events) != 1 {
		t.Fatalf("separate conversation was blocked: status=%d events=%d", w.Code, len(store.events))
	}
}

func TestSessionFreezeHistoryAssociationCoversAllMonitoredTextEndpoints(t *testing.T) {
	cases := []struct {
		name, path, first, next string
	}{
		{"chat", "/v1/chat/completions", `{"model":"test","messages":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"danger"}]}`, `{"model":"test","messages":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"danger"},{"role":"assistant","content":"denied"},{"role":"user","content":"continue"}]}`},
		{"responses", "/v1/responses", `{"model":"test","input":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"danger"}]}`, `{"model":"test","input":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"danger"},{"role":"assistant","content":"denied"},{"role":"user","content":"continue"}]}`},
		{"messages", "/v1/messages", `{"model":"test","messages":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"danger"}]}`, `{"model":"test","messages":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"danger"},{"role":"assistant","content":"denied"},{"role":"user","content":"continue"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := activePolicy()
			p.Scenes[0].SessionBlockEnabled, p.Scenes[0].SessionBlockTTLSeconds = true, 60
			c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": 0.9})}
			s, _, upstream := makeServer(t, p, c)
			first := authorizedRequest("POST", tc.path, strings.NewReader(tc.first))
			first.Header.Set("Authorization", "Bearer endpoint-key")
			first.Header.Set("User-Agent", "client/1.0")
			initial := httptest.NewRecorder()
			s.ServeHTTP(initial, first)
			if initial.Code != http.StatusForbidden {
				t.Fatalf("initial request was not blocked")
			}
			calls := c.calls
			c.answers = fullAnswers(nil)
			next := authorizedRequest("POST", tc.path, strings.NewReader(tc.next))
			next.Header.Set("Authorization", "Bearer endpoint-key")
			next.Header.Set("User-Agent", "client/1.9")
			w := httptest.NewRecorder()
			s.ServeHTTP(w, next)
			if w.Code != http.StatusForbidden || c.calls != calls || *upstream != 0 {
				t.Fatalf("continuation escaped freeze: status=%d calls=%d upstream=%d", w.Code, c.calls, *upstream)
			}
		})
	}
}

func TestSessionFreezeStorageFailureFailsOpenAndDoesNotPretendToFreeze(t *testing.T) {
	p := activePolicy()
	p.Scenes[0].SessionBlockEnabled, p.Scenes[0].SessionBlockTTLSeconds = true, 60
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": 0.9})}
	s, store, _ := makeServer(t, p, c)
	store.sessionErr = errors.New("redis unavailable")
	for i := 0; i < 2; i++ {
		r := authorizedRequest("POST", "/v1/responses", strings.NewReader(`{"input":"danger"}`))
		r.Header.Set("Authorization", "Bearer caller")
		r.Header.Set("Session-Id", "session")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("review result changed during storage failure: %d", w.Code)
		}
	}
	if c.calls != 2 || len(store.blocked) != 0 {
		t.Fatalf("storage failure fabricated a freeze: calls=%d blocks=%v", c.calls, store.blocked)
	}
}

func TestBlockEnvelopesMatchSub2apiEndpointBranches(t *testing.T) {
	for _, tc := range []struct{ endpoint, wantType string }{
		{"openai_chat", "permission_error"}, {"openai_responses", "api_error"},
		{"anthropic", "permission_error"}, {"openai_images_generations", "permission_error"},
		{"openai_images_edits", "permission_error"},
	} {
		t.Run(tc.endpoint, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeBlock(w, tc.endpoint, "request-abc", policy.DefaultBlockMessage)
			var response struct {
				Type  string
				Error struct{ Type, Code, Message string }
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if w.Code != 403 || response.Error.Type != tc.wantType || response.Error.Code != "prompt_guard_blocked" {
				t.Fatalf("unexpected rejection: %d %s", w.Code, w.Body.String())
			}
			if got := w.Header().Get("X-Sael-Blocked"); got != "prompt_guard" {
				t.Fatalf("missing Sael block marker: %q", got)
			}
			if (tc.endpoint == "anthropic") != (response.Type == "error") {
				t.Fatal("wrong outer envelope")
			}
			if strings.Contains(w.Body.String(), "cyber") || strings.Contains(w.Body.String(), "threshold") {
				t.Fatal("response leaks risk details")
			}
		})
	}
}

func TestBlockingFreezeDependsOnTheWinningScene(t *testing.T) {
	var p policy.Policy
	if err := json.Unmarshal([]byte(`{"enabled":true,"scenes":[{"id":"first","name":"first","match":"any","action":"block","conditions":[{"question":"cyber_abuse","threshold":0.5}]},{"id":"second","name":"second","match":"any","action":"block","session_block_enabled":true,"session_block_ttl_seconds":60,"conditions":[{"question":"gore","threshold":1}]}]}`), &p); err != nil {
		t.Fatal(err)
	}
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": 0.9, "gore": 2})}
	s, store, _ := makeServer(t, p, c)
	send := func() {
		r := authorizedRequest("POST", "/v1/responses", strings.NewReader(`{"input":"danger"}`))
		r.Header.Set("Authorization", "Bearer caller")
		r.Header.Set("Session-Id", "conversation")
		s.ServeHTTP(httptest.NewRecorder(), r)
	}
	send()
	if len(store.blocked) != 0 {
		t.Fatalf("shadowed scene froze a session: %v", store.blocked)
	}
	store.policy.Scenes[0], store.policy.Scenes[1] = store.policy.Scenes[1], store.policy.Scenes[0]
	send()
	if len(store.blocked) == 0 {
		t.Fatal("winning scene did not freeze the session")
	}
}

func TestBlockResponseUsesSavedMessageTemplateAndDefault(t *testing.T) {
	for _, tc := range []struct{ path, body string }{
		{"/v1/responses", `{"model":"test-model","input":"danger"}`},
		{"/v1/messages", `{"model":"test-model","messages":[{"role":"user","content":"danger"}]}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			p := activePolicy()
			p.BlockMessage = "Blocked {request_id} on {endpoint} ({model})"
			s, _, _ := makeServer(t, p, &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": 0.9})})
			w := httptest.NewRecorder()
			s.ServeHTTP(w, authorizedRequest("POST", tc.path, strings.NewReader(tc.body)))
			var got struct{ Error struct{ Message string } }
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			want := "Blocked " + w.Header().Get("X-Request-Id") + " on " + tc.path + " (test-model)"
			if got.Error.Message != want || w.Code != http.StatusForbidden {
				t.Fatalf("message=%q, want %q; status=%d", got.Error.Message, want, w.Code)
			}
		})
	}
}

func TestDefaultJevGuardSkipsVeryLongPromptWithoutClassifierFailure(t *testing.T) {
	c := &testClassifier{answers: fullAnswers(nil)}
	s, store, upstream := makeServer(t, activePolicy(), c)
	raw, _ := json.Marshal(map[string]string{"input": strings.Repeat(" a", 32000)})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/responses", strings.NewReader(string(raw))))
	if w.Code != 201 || *upstream != 1 || c.calls != 0 {
		t.Fatalf("oversized input was sent to Jev: calls=%d status=%d", c.calls, w.Code)
	}
	if len(store.events) != 1 || store.events[0].Kind != "warning" || store.events[0].Decision.Action != policy.Allow {
		t.Fatalf("missing warning or wrong action: %+v", store.events)
	}
	if store.counts[0].ClassifierSample || store.counts[0].ErrorKind != "" {
		t.Fatal("skipped request polluted Jev health")
	}
}

func TestJevGuardRejectsExplicitZeroAndUsesTokenUnits(t *testing.T) {
	s, _, _ := makeServer(t, activePolicy(), &testClassifier{})
	w := adminRequest(t, s, "PUT", "/admin/jev", "{\"base_url\":\"https://example.test\",\"api_key\":\"test\",\"model\":\"jev\",\"max_input_tokens\":0}")
	if w.Code != 400 {
		t.Fatalf("zero limit accepted: %d", w.Code)
	}
	w = adminRequest(t, s, "GET", "/admin/jev", "")
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["max_input_tokens"] != float64(28800) {
		t.Fatalf("missing default token limit: %s", w.Body.String())
	}
}

func TestDraftTextTestUsesProductionInputGuard(t *testing.T) {
	c := &testClassifier{answers: fullAnswers(nil)}
	s, store, _ := makeServer(t, activePolicy(), c)
	store.jev = JevConfig{BaseURL: "https://example.test", Model: "jev", APIKey: "secret", MaxInputTokens: 3}
	for _, path := range []string{"/admin/jev/test", "/admin/policy/test"} {
		w := adminRequest(t, s, "POST", path, "{\"text\":\"a b c d e f g\"}")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "\"skipped\":true") || c.calls != 0 {
			t.Fatalf("draft guard bypassed: %s %d %s", path, w.Code, w.Body.String())
		}
	}
	if len(store.events) != 0 || len(store.counts) != 0 {
		t.Fatal("draft guard wrote production records")
	}
}

func TestImageEditReviewsOnlyPromptAndPreservesMultipartRequest(t *testing.T) {
	var raw bytes.Buffer
	multi := multipart.NewWriter(&raw)
	if err := multi.WriteField("model", "gpt-image-1"); err != nil {
		t.Fatal(err)
	}
	if err := multi.WriteField("prompt", "draw a lake"); err != nil {
		t.Fatal(err)
	}
	file, err := multi.CreateFormFile("image[]", "original.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{0x89, 0x50, 0x4e, 0x47, 0, 0xff, 0x0d, 0x0a}); err != nil {
		t.Fatal(err)
	}
	if err := multi.Close(); err != nil {
		t.Fatal(err)
	}
	var gotBody []byte
	var gotContentType, gotKey, gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotContentType, gotKey, gotQuery = r.Header.Get("Content-Type"), r.Header.Get("Authorization"), r.URL.RawQuery
		w.WriteHeader(http.StatusCreated)
	}))
	defer upstream.Close()
	c := &testClassifier{answers: fullAnswers(nil)}
	s, store, _ := makeServer(t, activePolicy(), c)
	store.upstream.BaseURL = upstream.URL
	r := authorizedRequest("POST", "/v1/images/edits?trace=kept", bytes.NewReader(raw.Bytes()))
	r.Header.Set("Content-Type", multi.FormDataContentType())
	r.Header.Set("Authorization", "Bearer image-caller")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 201 || c.calls != 1 || c.text != "draw a lake" {
		t.Fatalf("image content entered review or request failed: status=%d text=%q calls=%d", w.Code, c.text, c.calls)
	}
	if !bytes.Equal(raw.Bytes(), gotBody) || gotContentType != multi.FormDataContentType() || gotKey != "Bearer image-caller" || gotQuery != "trace=kept" {
		t.Fatal("relay modified multipart body, boundary, credentials or query")
	}
}

func TestFrozenSessionRespectsSceneScopeAndDoesNotRenewOnRetry(t *testing.T) {
	p := activePolicy()
	p.Scenes[0].SessionBlockEnabled, p.Scenes[0].SessionBlockTTLSeconds = true, 60
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": .9})}
	s, store, _ := makeServer(t, p, c)
	call := func(path string) int {
		r := authorizedRequest("POST", path, strings.NewReader(`{"input":"hello"}`))
		r.Header.Set("Authorization", "Bearer owner")
		r.Header.Set("Session-Id", "conversation")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w.Code
	}
	if call("/v1/responses") != 403 {
		t.Fatal("initial block missing")
	}
	var key string
	var until time.Time
	for k, v := range store.blocked {
		key, until = k, v
	}
	c.answers = fullAnswers(nil)
	if call("/v1/responses") != 403 || !store.blocked[key].Equal(until) {
		t.Fatal("retry renewed freeze")
	}
	before := len(store.counts)
	if call("/v1/embeddings") != 201 || len(store.counts) != before {
		t.Fatal("unmonitored endpoint entered review")
	}
	store.policy.Enabled = false
	if call("/v1/responses") != 201 {
		t.Fatal("review-off did not bypass freeze")
	}
	store.policy.Enabled = true
	store.policy.Scenes[0].Endpoints = []string{"openai_chat"}
	if call("/v1/responses") != 201 {
		t.Fatal("endpoint outside scene scope did not bypass freeze")
	}
	store.policy.Scenes[0].Endpoints = nil
	store.policy.Scenes[0].SessionBlockEnabled = false
	if call("/v1/responses") != 201 || c.calls != 2 {
		t.Fatal("freeze-off did not resume normal review")
	}
}

func TestJevInputAtLimitIsReviewedAndOverLimitIsSkipped(t *testing.T) {
	c := &testClassifier{answers: fullAnswers(nil)}
	s, store, _ := makeServer(t, activePolicy(), c)
	store.jev.MaxInputTokens = 3
	for _, tc := range []struct {
		text      string
		wantCalls int
		outcome   string
	}{
		{"a b c", 1, "clean"}, {"a b c d", 1, "input_too_long"},
	} {
		raw, _ := json.Marshal(map[string]string{"input": tc.text})
		w := httptest.NewRecorder()
		s.ServeHTTP(w, authorizedRequest("POST", "/v1/responses", bytes.NewReader(raw)))
		if w.Code != 201 || c.calls != tc.wantCalls || store.counts[len(store.counts)-1].Outcome != tc.outcome {
			t.Fatalf("wrong limit boundary for %q", tc.text)
		}
	}
}

func TestNormalRequestKeepsAggregatesWithoutScoresOrAuditRecord(t *testing.T) {
	p := activePolicy()
	chars := 200
	p.PreviewChars = &chars
	c := &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": .1})}
	s, store, upstream := makeServer(t, p, c)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/responses", strings.NewReader(`{"model":"test","input":"ordinary current user text"}`)))
	if w.Code != 201 || *upstream != 1 || len(store.events) != 0 {
		t.Fatal("normal traffic must forward without an audit record")
	}
	if len(store.counts) != 1 || store.counts[0].Outcome != "clean" || !store.counts[0].ClassifierSample {
		t.Fatal("normal traffic lost its aggregate count or latency sample")
	}
	if len(store.counts[0].Scores) != 0 || len(store.counts[0].SceneMatches) != 0 {
		t.Fatal("normal traffic retained classifier scores or match details")
	}
}

func TestByteLengthDoesNotRejectTextBelowTokenLimit(t *testing.T) {
	c := &testClassifier{answers: fullAnswers(nil)}
	s, store, _ := makeServer(t, activePolicy(), c)
	store.jev.MaxInputTokens = 3
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest("POST", "/v1/responses", strings.NewReader(`{"input":"hello world"}`)))
	if w.Code != 201 || c.calls != 1 || len(store.events) != 0 {
		t.Fatal("byte size was incorrectly used as the token limit")
	}
}

type ingressCaptureStore struct {
	*testStore
	ingress []Count
}

func (s *ingressCaptureStore) ObserveIngress(_ context.Context, c Count) error {
	s.ingress = append(s.ingress, c)
	return nil
}

type waitingClassifier struct{ started, release chan struct{} }

func (c waitingClassifier) Check(context.Context, string) ([]policy.Answer, error) {
	close(c.started)
	<-c.release
	return fullAnswers(nil), nil
}
func TestIngressRateIsObservedBeforeClassificationCompletes(t *testing.T) {
	c := waitingClassifier{make(chan struct{}), make(chan struct{})}
	s, st, _ := makeServer(t, activePolicy(), &testClassifier{})
	s.Classifier = c
	capture := &ingressCaptureStore{testStore: st}
	s.Store = capture
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.ServeHTTP(httptest.NewRecorder(), authorizedRequest("POST", "/v1/responses", strings.NewReader(`{"model":"test-model","input":"hello"}`)))
	}()
	<-c.started
	if len(capture.ingress) != 1 || capture.ingress[0].Model != "test-model" || capture.ingress[0].Protocol != "openai_responses" {
		t.Error("RPM waited for classification or lost endpoint/model")
	}
	if len(st.counts) != 0 {
		t.Error("decision counted before classification")
	}
	close(c.release)
	<-done
	if len(capture.ingress) != 1 || len(st.counts) != 1 {
		t.Fatal("ingress or outcome counted twice")
	}
}
