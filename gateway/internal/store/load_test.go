package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cipherTing/sael/gateway/internal/classifier"
	"github.com/cipherTing/sael/gateway/internal/gateway"
	"github.com/cipherTing/sael/gateway/internal/policy"
	"github.com/cipherTing/sael/gateway/internal/testcli"
)

// TestGatewayLoad is opt-in and requires isolated TEST_DATABASE_URL / TEST_REDIS_URL.
// It exercises actual HTTP, a persistent CLI process, Redis persistence, and PostgreSQL ingestion.
func TestGatewayLoad(t *testing.T) {
	if os.Getenv("SAEL_LOAD_TEST") != "1" {
		t.Skip("set SAEL_LOAD_TEST=1 to run the 5000 RPS acceptance workload")
	}
	value := func(key string, fallback int) int {
		n, _ := strconv.Atoi(os.Getenv(key))
		if n > 0 {
			return n
		}
		return fallback
	}
	rate, seconds, size := value("SAEL_LOAD_RPS", 5000), value("SAEL_LOAD_SECONDS", 20), value("SAEL_LOAD_BODY_BYTES", 1024)
	mode := os.Getenv("SAEL_LOAD_MODE")
	cacheEnabled := strings.HasPrefix(mode, "cache-")
	mode = strings.TrimPrefix(mode, "cache-")
	if mode == "" {
		mode = "mixed"
	}
	p, r := redisFixture(t)
	ctx := context.Background()
	if err := p.InitCredentials(ctx, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))); err != nil {
		t.Fatal(err)
	}
	var providerCalls, providerHits, forwarded, providerConnections atomic.Int64
	response := func(hit bool) []byte {
		answers := map[string]any{}
		for _, q := range policy.Questions {
			v := .1
			if hit && q.Key == "cyber_abuse" {
				v = .9
			}
			if q.Type == "score" {
				answers[q.Key] = map[string]any{"type": "score", "score": v}
			} else {
				answers[q.Key] = map[string]any{"type": "noul", "noul": v}
			}
		}
		raw, _ := json.Marshal(map[string]any{"answers": answers})
		return raw
	}
	cleanAnswer, hitAnswer := response(false), response(true)
	provider := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		providerCalls.Add(1)
		raw, _ := io.ReadAll(req.Body)
		if bytes.Contains(raw, []byte(`"state":"load:hit`)) {
			providerHits.Add(1)
			_, _ = w.Write(hitAnswer)
		} else {
			_, _ = w.Write(cleanAnswer)
		}
	}))
	provider.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			providerConnections.Add(1)
		}
	}
	provider.Start()
	defer provider.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		forwarded.Add(1)
		_, _ = io.Copy(io.Discard, req.Body)
		if mode == "sse" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: chunk\n\n")
			w.(http.Flusher).Flush()
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		} else {
			_, _ = io.WriteString(w, `{"ok":true}`)
		}
	}))
	defer upstream.Close()
	if _, err := p.UpdateUpstream(ctx, gateway.UpstreamConfig{BaseURL: upstream.URL}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpdateJev(ctx, gateway.JevConfig{BaseURL: provider.URL, APIKey: "load-fixture", Model: "fixed-response", TimeoutMS: 5000}); err != nil {
		t.Fatal(err)
	}
	chars := 200
	next := policy.Policy{Enabled: mode != "disabled", PreviewChars: &chars, Scenes: []policy.Scene{{ID: "load-scene", Name: "Load", Match: policy.Any, Action: policy.Block, Conditions: []policy.Condition{{Question: "cyber_abuse", Threshold: .5}}}}}
	_, err := p.UpdatePolicy(ctx, gateway.PolicyUpdate{Replace: &next}, "load-test")
	if err != nil {
		t.Fatal(err)
	}
	storage, err := OpenRedis(ctx, p, os.Getenv("TEST_REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	rawKey, _ := json.Marshal([2]string{upstream.URL, "load-key"})
	fingerprint := sha256.Sum256(rawKey)
	if err := storage.RememberKey(ctx, hex.EncodeToString(fingerprint[:]), 30*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	detector, err := classifier.NewCLI(ctx, testcli.Binary(t))
	if err != nil {
		t.Fatal(err)
	}
	defer detector.Close()
	api := gateway.New(storage, detector, "load-test")
	defer api.Close()
	if cacheEnabled {
		cacheURL := os.Getenv("TEST_REVIEW_CACHE_URL")
		if cacheURL == "" {
			t.Fatal("TEST_REVIEW_CACHE_URL required")
		}
		cache, err := OpenReviewCache(ctx, p, cacheURL, os.Getenv("TEST_REDIS_URL"))
		if err != nil {
			t.Fatal(err)
		}
		defer cache.Close()
		api.ReviewCache = cache
	}
	ingress := httptest.NewServer(api)
	defer ingress.Close()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 512
	transport.MaxIdleConnsPerHost = 512
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	body := func(hit bool) []byte {
		text := "load:normal "
		if hit {
			text = "load:hit "
		}
		raw, _ := json.Marshal(map[string]any{"input": text + strings.Repeat("ordinary prompt ", size/16), "model": "load-test", "stream": mode == "sse"})
		return raw
	}
	cleanBody, hitBody := body(false), body(true)
	type job struct {
		index int
		at    time.Time
	}
	total := rate * seconds
	latencies := make([]time.Duration, total)
	jobs := make(chan job, 512)
	var failures, expectedHits atomic.Int64
	var workers sync.WaitGroup
	for i := 0; i < 256; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := range jobs {
				hit := mode == "hit" || (mode == "mixed" && j.index%20 == 0)
				payload, status := cleanBody, 200
				if hit {
					payload = hitBody
					status = 403
					expectedHits.Add(1)
				}
				req, _ := http.NewRequest("POST", ingress.URL+"/v1/responses", bytes.NewReader(payload))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer load-key")
				resp, err := client.Do(req)
				if err != nil {
					failures.Add(1)
				} else {
					_, copyErr := io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					if resp.StatusCode != status || copyErr != nil {
						failures.Add(1)
					}
				}
				latencies[j.index] = time.Since(j.at)
			}
		}()
	}
	start := time.Now()
	var maximumBacklog int64
	resources := []map[string]int64{}
	for i := 0; i < total; i++ {
		scheduled := start.Add(time.Duration(i) * time.Second / time.Duration(rate))
		if delay := time.Until(scheduled); delay > 0 {
			time.Sleep(delay)
		}
		jobs <- job{i, scheduled}
		if i%rate == 0 {
			if n := r.XLen(ctx, ingestStream).Val(); n > maximumBacklog {
				maximumBacklog = n
			}
			if i%(rate*5) == 0 {
				var sample runtime.MemStats
				runtime.ReadMemStats(&sample)
				fds, fdErr := os.ReadDir("/dev/fd")
				if fdErr != nil {
					fds, fdErr = os.ReadDir("/proc/self/fd")
				}
				openFDs := int64(-1)
				if fdErr == nil {
					openFDs = int64(len(fds))
				}
				resources = append(resources, map[string]int64{"second": int64(i / rate), "goroutines": int64(runtime.NumGoroutine()), "heap_mb": int64(sample.HeapAlloc / 1024 / 1024), "open_fds": openFDs, "provider_connections": providerConnections.Load()})
			}
		}
	}
	close(jobs)
	workers.Wait()
	elapsed := time.Since(start)
	deadline := time.Now().Add(15 * time.Second)
	var stored, events int64
	for {
		_ = p.pool.QueryRow(ctx, "SELECT coalesce(sum(count),0) FROM gateway_counts_minute").Scan(&stored)
		if stored == int64(total) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("aggregation did not catch up: %d/%d", stored, total)
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = p.pool.QueryRow(ctx, "SELECT count(*) FROM audit_events").Scan(&events)
	if events != expectedHits.Load() {
		t.Errorf("unexpected detailed records: got %d want %d", events, expectedHits.Load())
	}
	var cleanScores int64
	_ = p.pool.QueryRow(ctx, "SELECT coalesce(sum(count),0) FROM gateway_measurements_minute WHERE metric='hit_score:cyber_abuse'").Scan(&cleanScores)
	if cleanScores != providerHits.Load() {
		t.Errorf("normal traffic leaked into score distribution: %d", cleanScores)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	result := map[string]any{"mode": mode, "cache_enabled": cacheEnabled, "provider_connections": providerConnections.Load(), "goroutines": runtime.NumGoroutine(), "request_bytes": len(cleanBody), "requests": total, "elapsed_seconds": elapsed.Seconds(), "rps": float64(total) / elapsed.Seconds(), "failures": failures.Load(), "p50_ms": float64(latencies[total/2].Microseconds()) / 1000, "p95_ms": float64(latencies[total*95/100].Microseconds()) / 1000, "p99_ms": float64(latencies[total*99/100].Microseconds()) / 1000, "stored": stored, "events": events, "forwarded": forwarded.Load(), "provider_calls": providerCalls.Load(), "redis_peak_batches": maximumBacklog, "redis_remaining_batches": r.XLen(ctx, ingestStream).Val(), "heap_mb": mem.HeapAlloc / 1024 / 1024, "total_allocated_mb": mem.TotalAlloc / 1024 / 1024}
	result["resource_samples"] = resources
	raw, _ := json.Marshal(result)
	fmt.Println("LOAD_RESULT", string(raw))
	if failures.Load() != 0 {
		t.Errorf("HTTP failures: %d", failures.Load())
	}
	if float64(total)/elapsed.Seconds() < float64(rate)*.98 {
		t.Errorf("target %d RPS not sustained", rate)
	}
}
