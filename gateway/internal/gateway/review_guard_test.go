package gateway

import (
	"bytes"
	"context"
	"encoding/json"
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
	p.SessionBlockEnabled, p.SessionBlockTTLSeconds = true, 60
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
	p.SessionBlockEnabled, p.SessionBlockTTLSeconds = true, 60
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

func TestBlockEnvelopesMatchSub2apiEndpointBranches(t *testing.T) {
	for _, tc := range []struct{ endpoint, wantType string }{
		{"openai_chat", "permission_error"}, {"openai_responses", "api_error"},
		{"anthropic", "permission_error"}, {"openai_images_generations", "permission_error"},
		{"openai_images_edits", "permission_error"},
	} {
		t.Run(tc.endpoint, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeBlock(w, tc.endpoint, "request-abc")
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
			if (tc.endpoint == "anthropic") != (response.Type == "error") {
				t.Fatal("wrong outer envelope")
			}
			if strings.Contains(w.Body.String(), "cyber") || strings.Contains(w.Body.String(), "threshold") {
				t.Fatal("response leaks risk details")
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
	p.SessionBlockEnabled, p.SessionBlockTTLSeconds = true, 60
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
	store.policy.SessionBlockEnabled = false
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
