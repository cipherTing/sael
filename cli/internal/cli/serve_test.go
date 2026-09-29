package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cipherTing/sael/cli/internal/questions"
)

type serviceFixture struct {
	address, token string
	owner          *io.PipeWriter
	done           <-chan error
}

func startService(t *testing.T) serviceFixture {
	t.Helper()
	in, owner := io.Pipe()
	out, ready := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	root := newRootCmd()
	root.SetContext(ctx)
	root.SetArgs([]string{"serve"})
	root.SetIn(in)
	root.SetOut(ready)
	root.SetErr(io.Discard)
	done := make(chan error, 1)
	go func() {
		err := root.Execute()
		_ = ready.CloseWithError(err)
		done <- err
	}()
	t.Cleanup(func() { cancel(); _ = owner.Close(); _ = in.Close(); _ = out.Close() })
	var handshake struct {
		Version        int
		Address, Token string
	}
	if err := json.NewDecoder(out).Decode(&handshake); err != nil {
		t.Fatalf("CLI did not start persistent service: %v", err)
	}
	host, _, err := net.SplitHostPort(handshake.Address)
	if err != nil || host != "127.0.0.1" || handshake.Version != 2 || len(handshake.Token) < 32 {
		t.Fatal("invalid private service handshake")
	}
	return serviceFixture{handshake.Address, handshake.Token, owner, done}
}

func serviceRequest(ctx context.Context, t *testing.T, s serviceFixture, text, endpoint, key, model string, authenticated bool) *http.Response {
	t.Helper()
	keys := make([]string, 0, len(questions.Moderation()))
	for key := range questions.Moderation() {
		keys = append(keys, key)
	}
	return serviceRequestSelected(ctx, t, s, text, endpoint, key, model, keys, authenticated)
}

func serviceRequestSelected(ctx context.Context, t *testing.T, s serviceFixture, text, endpoint, key, model string, selected []string, authenticated bool) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"text": text, "base_url": endpoint, "api_key": key, "model": model, "deadline": time.Now().Add(2 * time.Second), "questions": selected})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+s.address+"/check", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if authenticated {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestServeUsesAnIndependentQuestionSelectionForEachRequest(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			State     string                     `json:"state"`
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		answers := map[string]any{}
		for key := range body.Questions {
			if key == "gore" {
				answers[key] = map[string]any{"type": "score", "score": 1.5}
			} else {
				answers[key] = map[string]any{"type": "noul", "noul": 0.9}
			}
		}
		if body.State == "first" {
			close(entered)
			<-release
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": answers})
	}))
	defer upstream.Close()
	s := startService(t)
	firstDone := make(chan []string, 1)
	go func() {
		resp := serviceRequestSelected(context.Background(), t, s, "first", upstream.URL, "key", "model", []string{"gore"}, true)
		defer resp.Body.Close()
		var result struct {
			Answers []struct {
				Question string `json:"question"`
			} `json:"answers"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&result)
		out := make([]string, 0, len(result.Answers))
		for _, answer := range result.Answers {
			out = append(out, answer.Question)
		}
		firstDone <- out
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first subset did not reach Jev")
	}
	second := serviceRequestSelected(context.Background(), t, s, "second", upstream.URL, "key", "model", []string{"self_harm"}, true)
	var result struct {
		Answers []struct {
			Question string `json:"question"`
		} `json:"answers"`
	}
	if err := json.NewDecoder(second.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	_ = second.Body.Close()
	if second.StatusCode != 200 || len(result.Answers) != 1 || result.Answers[0].Question != "self_harm" {
		t.Fatalf("second request used the wrong question set: status=%d answers=%+v", second.StatusCode, result.Answers)
	}
	close(release)
	if got := <-firstDone; len(got) != 1 || got[0] != "gore" {
		t.Fatalf("first request used the wrong question set: %v", got)
	}
}

func TestServeRejectsMissingAndDuplicateQuestionSelectionBeforeJev(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid selection reached Jev")
	}))
	defer upstream.Close()
	s := startService(t)
	for _, keys := range [][]string{nil, {}, {"gore", "gore"}, {"missing"}} {
		resp := serviceRequestSelected(context.Background(), t, s, "text", upstream.URL, "key", "model", keys, true)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("questions %v returned %d", keys, resp.StatusCode)
		}
	}
}

func serveAnswer(w http.ResponseWriter) {
	answers := map[string]any{}
	for key := range questions.Moderation() {
		if key == "sexual" || key == "gore" {
			answers[key] = map[string]any{"type": "score", "score": 1.5}
		} else {
			answers[key] = map[string]any{"type": "noul", "noul": .2}
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"answers": answers})
}

func TestServeKeepsOneAuthenticatedSessionAndPoolsUpstreamConnections(t *testing.T) {
	var calls, connections atomic.Int64
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			State, Model string
			Questions    map[string]json.RawMessage
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.State != "  原始文本\n" || body.Model != "m" || len(body.Questions) != 11 || r.Header.Get("Authorization") != "Bearer private-key" {
			t.Error("changed prompt, questions or request configuration")
		}
		serveAnswer(w)
	}))
	upstream.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	upstream.Start()
	defer upstream.Close()
	s := startService(t)
	unauthorized := serviceRequest(context.Background(), t, s, "ignored", upstream.URL, "private-key", "m", false)
	_ = unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized || calls.Load() != 0 {
		t.Fatal("unauthenticated local client reached Jev")
	}
	for range 20 {
		resp := serviceRequest(context.Background(), t, s, "  原始文本\n", upstream.URL, "private-key", "m", true)
		var body struct {
			Answers []struct {
				Question, Type string
				Value          float64
			}
			Error string
		}
		err := json.NewDecoder(resp.Body).Decode(&body)
		_ = resp.Body.Close()
		if err != nil || body.Error != "" || len(body.Answers) != 11 {
			t.Fatalf("invalid service answer: %+v %v", body, err)
		}
		for _, a := range body.Answers {
			if a.Type == "score" && a.Value != 1.5 {
				t.Fatal("fractional score changed")
			}
		}
	}
	if calls.Load() != 20 || connections.Load() != 1 {
		t.Fatalf("lost request or connection reuse: %d calls, %d connections", calls.Load(), connections.Load())
	}
	_ = s.owner.Close()
	select {
	case err := <-s.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CLI did not exit after owner disconnected")
	}
}

func TestServeSlowRequestDoesNotSerializeNewConfiguration(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		serveAnswer(w)
	}))
	defer first.Close()
	defer close(release)
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if r.Header.Get("Authorization") != "Bearer new-key" || !strings.Contains(string(raw), `"model":"new-model"`) {
			t.Error("configuration leaked between requests")
		}
		serveAnswer(w)
	}))
	defer second.Close()
	s := startService(t)
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		resp := serviceRequest(context.Background(), t, s, "slow", first.URL, "old-key", "old-model", true)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first request did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	resp := serviceRequest(ctx, t, s, "fast", second.URL, "new-key", "new-model", true)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatal("slow request blocked independent request")
	}
}
