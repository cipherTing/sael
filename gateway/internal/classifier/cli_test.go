package classifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cipherTing/sael/gateway/internal/gateway"
	"github.com/cipherTing/sael/gateway/internal/policy"
	"github.com/cipherTing/sael/gateway/internal/testcli"
)

func checkFull(c *CLI, ctx context.Context, text string, cfg gateway.JevConfig) ([]policy.Answer, error) {
	keys := make([]string, 0, len(policy.Questions))
	for _, question := range policy.Questions {
		keys = append(keys, question.Key)
	}
	return c.CheckConfigured(ctx, text, cfg, keys)
}

func TestCLIUsesSavedConnectionAndPreservesMeasurements(t *testing.T) {
	calls := 0
	extraAnswer := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			State, Model string
			Questions    map[string]json.RawMessage
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/v1/systemone" || body.State != "current user prompt" || body.Model != "saved-model" || len(body.Questions) != 11 || r.Header.Get("Authorization") != "Bearer saved-key" {
			t.Error("changed classifier request")
		}
		answers := map[string]any{}
		for _, q := range policy.Questions {
			if q.Type == "score" {
				answers[q.Key] = map[string]any{"type": "score", "score": 1.5}
			} else {
				answers[q.Key] = map[string]any{"type": "noul", "noul": .2}
			}
		}
		if extraAnswer {
			answers["unexpected"] = map[string]any{"type": "noul", "noul": .2}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": answers})
	}))
	defer upstream.Close()
	c := startCLI(t)
	for i := 0; i < 2; i++ {
		a, err := checkFull(c, context.Background(), "current user prompt", gateway.JevConfig{BaseURL: upstream.URL + "/v1", APIKey: "saved-key", Model: "saved-model"})
		if err != nil {
			t.Fatal(err)
		}
		if err := policy.ValidateAnswers(a); err != nil {
			t.Fatal(err)
		}
		for _, v := range a {
			if v.Type == "score" && v.Value != 1.5 {
				t.Fatal("rounded fractional score")
			}
		}
	}
	if calls != 2 {
		t.Fatal("unexpected retries")
	}
	extraAnswer = true
	if _, err := checkFull(c, context.Background(), "current user prompt", gateway.JevConfig{BaseURL: upstream.URL + "/v1", APIKey: "saved-key", Model: "saved-model"}); !errors.Is(err, gateway.ErrInvalidClassifierResponse) {
		t.Fatal("unexpected answer silently ignored")
	}
}

func TestCLIForwardsOnlyTheRequestedQuestionSubset(t *testing.T) {
	var got map[string]json.RawMessage
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		got = body.Questions
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{
			"gore":      map[string]any{"type": "score", "score": 1.5},
			"self_harm": map[string]any{"type": "noul", "noul": .9},
		}})
	}))
	defer upstream.Close()
	c := startCLI(t)
	answers, err := c.CheckConfigured(context.Background(), "prompt", gateway.JevConfig{BaseURL: upstream.URL, APIKey: "key", Model: "model"}, []string{"gore", "self_harm"})
	if err != nil || len(answers) != 2 || len(got) != 2 {
		t.Fatalf("subset request failed: answers=%+v body=%v err=%v", answers, got, err)
	}
	if _, ok := got["sexual"]; ok {
		t.Fatal("unrequested question was sent")
	}
}
func TestCLIPropagatesCancellationAndInvalidAnswer(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"answers":{}}`)) }))
	defer upstream.Close()
	c := startCLI(t)
	cfg := gateway.JevConfig{BaseURL: upstream.URL, APIKey: "key", Model: "model"}
	if _, err := checkFull(c, context.Background(), "text", cfg); !errors.Is(err, gateway.ErrInvalidClassifierResponse) {
		t.Fatal("missing answers not classified", err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, err := checkFull(c, ctx, "text", cfg); err == nil {
		t.Fatal("expired request sent")
	}
}

func cliTestResponse(w http.ResponseWriter) {
	answers := map[string]any{}
	for _, q := range policy.Questions {
		if q.Type == "score" {
			answers[q.Key] = map[string]any{"type": "score", "score": 1.5}
		} else {
			answers[q.Key] = map[string]any{"type": "noul", "noul": .2}
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"answers": answers})
}
func TestCLIConcurrentRequestsKeepTheirConfigurationSnapshots(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; cliTestResponse(w) }))
	defer first.Close()
	defer unblock()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer second-key" {
			t.Error("credential crossed configuration snapshots")
		}
		cliTestResponse(w)
	}))
	defer second.Close()
	c := startCLI(t)
	defer c.Close()
	firstDone := make(chan error, 1)
	go func() {
		_, err := checkFull(c, context.Background(), "first", gateway.JevConfig{BaseURL: first.URL, APIKey: "first-key", Model: "one"})
		firstDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first request did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := checkFull(c, ctx, "second", gateway.JevConfig{BaseURL: second.URL, APIKey: "second-key", Model: "two"}); err != nil {
		t.Fatal("slow request serialized a new configuration", err)
	}
	unblock()
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}
func TestCLIReusesConnectionsAndBoundsRetryWaitByCallerDeadline(t *testing.T) {
	var connections, calls atomic.Int64
	var failing atomic.Bool
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		if failing.Load() {
			w.WriteHeader(503)
			return
		}
		cliTestResponse(w)
	}))
	upstream.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			connections.Add(1)
		}
	}
	upstream.Start()
	defer upstream.Close()
	c := startCLI(t)
	defer c.Close()
	cfg := gateway.JevConfig{BaseURL: upstream.URL, APIKey: "key", Model: "model"}
	for range 20 {
		if _, err := checkFull(c, context.Background(), "prompt", cfg); err != nil {
			t.Fatal(err)
		}
	}
	if connections.Load() != 1 {
		t.Fatalf("lost pooling: %d connections", connections.Load())
	}
	failing.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := checkFull(c, ctx, "prompt", cfg); err == nil {
		t.Fatal("503 unexpectedly succeeded")
	}
	if time.Since(start) > 250*time.Millisecond || calls.Load() != 21 {
		t.Fatal("retry wait escaped the request deadline")
	}
}
func BenchmarkPersistentCLI(b *testing.B) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); cliTestResponse(w) }))
	defer upstream.Close()
	c := startCLI(b)
	defer c.Close()
	cfg := gateway.JevConfig{BaseURL: upstream.URL, APIKey: "benchmark-only", Model: "mock"}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := checkFull(c, context.Background(), "ordinary prompt", cfg); err != nil {
				b.Error(err)
			}
		}
	})
}

func startCLI(t testing.TB) *CLI {
	t.Helper()
	c, err := NewCLI(context.Background(), testcli.Binary(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

func TestCLICancellationOnlyStopsTheCanceledRequest(t *testing.T) {
	entered, canceled := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if string(raw) != "" && bytes.Contains(raw, []byte(`"state":"slow"`)) {
			close(entered)
			<-r.Context().Done()
			close(canceled)
			return
		}
		cliTestResponse(w)
	}))
	defer upstream.Close()
	c := startCLI(t)
	defer c.Close()
	cfg := gateway.JevConfig{BaseURL: upstream.URL, APIKey: "key", Model: "model"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := checkFull(c, ctx, "slow", cfg); finished <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request not sent through CLI")
	}
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("Jev request continued after gateway canceled")
	}
	if _, err := checkFull(c, context.Background(), "next", cfg); err != nil {
		t.Fatal("canceling one call stopped the shared CLI", err)
	}
	c.Close()
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("CLI process survived gateway close")
	}
	if c.cmd.ProcessState == nil || !c.cmd.ProcessState.Success() {
		t.Fatal("CLI was killed instead of exiting with owner EOF")
	}
}

func TestCLIProcessFailureIsReportedWithoutDirectSDKFallback(t *testing.T) {
	c := startCLI(t)
	if err := c.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("process failure was not observed")
	}
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); cliTestResponse(w) }))
	defer upstream.Close()
	_, err := checkFull(c, context.Background(), "prompt", gateway.JevConfig{BaseURL: upstream.URL, APIKey: "key", Model: "m"})
	if err == nil || calls.Load() != 0 {
		t.Fatal("request bypassed the failed CLI")
	}
}

func TestCLIResponseReadTimeoutKeepsItsErrorCategory(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, `{"answers":`)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer peer.Close()
	c := &CLI{client: peer.Client(), address: strings.TrimPrefix(peer.URL, "http://"), done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := checkFull(c, ctx, "prompt", gateway.JevConfig{BaseURL: "http://example.test", APIKey: "key", Model: "m"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("IPC response timeout was reclassified: %v", err)
	}
}
