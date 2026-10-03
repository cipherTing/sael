package store

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/cipherTing/sael/gateway/internal/gateway"
	"github.com/cipherTing/sael/gateway/internal/policy"
	"github.com/cipherTing/sael/gateway/internal/protocol"
)

func TestPostgresStorageReplacesNullBytesWithSpaces(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
	}{
		{"null between words", "before\x00after", "before after"},
		{"repeated nulls", "\x00\x00", "  "},
		{"literal escape", `before\u0000after`, `before\u0000after`},
		{"escaped slash before null", "\\\x00", "\\ "},
		{"ordinary text", "中文🙂\ntext", "中文🙂\ntext"},
		{"invalid UTF-8", "\x00" + string([]byte{0xff}), " �"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := postgresText(tc.input); got != tc.want {
				t.Fatalf("stored text=%q want=%q", got, tc.want)
			}
			value := map[string]string{tc.input: tc.input}
			raw, err := marshalPostgresJSON(value)
			if err != nil {
				t.Fatal(err)
			}
			var stored map[string]string
			if err := json.Unmarshal(raw, &stored); err != nil {
				t.Fatal(err)
			}
			if len(stored) != 1 || stored[tc.want] != tc.want {
				t.Fatalf("stored JSON=%q want key and value=%q", raw, tc.want)
			}
			if value[tc.input] != tc.input {
				t.Fatal("storage cleanup changed the caller's data")
			}
		})
	}
}

func TestTelemetryConsumesExistingNullBytesWithoutLosingLaterBatches(t *testing.T) {
	p, r := redisFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	event := gateway.Event{
		ID: "unicode-problem", Time: now, Kind: "hit", RequestID: "req\x00one",
		Protocol: "openai_chat", Model: "mo\x00del", UserAgent: "client\x00/1.0",
		Text:        "real:\x00; literal:\\u0000; 中文🙂\npassword=private-test",
		TextPreview: "real:\x00", SessionID: "session\x00one",
		Parameters: protocol.Parameters{ServiceTier: "tier\x00one"},
		Decision:   policy.Decision{Action: policy.Block, SceneID: "scene", SceneName: "risk\x00one"},
	}
	event.Redact()
	key := aggregateKey{Kind: "count", Bucket: now.Truncate(time.Minute), Protocol: "openai_chat", Model: "mo\x00del", Outcome: "blocked"}
	// These are already-persisted batches from an older publisher, before any cleanup.
	bad := ingestBatch{Producer: "old", Sequence: 1, Rows: []aggregateRow{{aggregateKey: key, Count: 2, LastSeen: now}}, Events: []gateway.Event{event}}
	key.Model = "mo del"
	good := ingestBatch{Producer: "old", Sequence: 2, Rows: []aggregateRow{{aggregateKey: key, Count: 3, LastSeen: now}}, Events: []gateway.Event{{ID: "later", Time: now, Kind: "warning", RequestID: "later", Decision: policy.Decision{Action: policy.Allow}}}}
	last := ""
	for _, b := range []ingestBatch{bad, good} {
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		last, err = r.XAdd(ctx, &redis.XAddArgs{Stream: ingestStream, Values: map[string]any{"data": string(raw)}}).Result()
		if err != nil {
			t.Fatal(err)
		}
	}
	s := &RedisStore{PG: p, redis: r}
	if more, err := s.consume(ctx); err != nil || !more {
		t.Fatalf("existing batch blocked telemetry recovery: more=%v error=%v", more, err)
	}
	var stored gateway.Event
	var body []byte
	if err := p.pool.QueryRow(ctx, "SELECT body FROM audit_events WHERE id='unicode-problem'").Scan(&body); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Text != "real: ; literal:\\u0000; 中文🙂\npassword=[已隐藏]" || stored.RequestID != "req one" || stored.Model != "mo del" || stored.Parameters.ServiceTier != "tier one" || stored.UserAgent != "client /1.0" || stored.SessionID != "session one" || stored.Decision.SceneName != "risk one" {
		t.Fatalf("stored event was not cleaned safely: %+v", stored)
	}
	var counts, rows, events int64
	if err := p.pool.QueryRow(ctx, "SELECT sum(count),count(*) FROM gateway_counts_minute WHERE model='mo del'").Scan(&counts, &rows); err != nil {
		t.Fatal(err)
	}
	if err := p.pool.QueryRow(ctx, "SELECT count(*) FROM audit_events").Scan(&events); err != nil {
		t.Fatal(err)
	}
	if counts != 5 || rows != 1 || events != 2 {
		t.Fatalf("recovery lost records or failed to merge normalized keys: counts=%d rows=%d events=%d", counts, rows, events)
	}
	if cursor, err := p.cursor(ctx); err != nil || cursor != last {
		t.Fatalf("consumer did not advance: cursor=%s want=%s error=%v", cursor, last, err)
	}
	if more, err := s.consume(ctx); err != nil || more {
		t.Fatalf("committed batches were retried: more=%v error=%v", more, err)
	}
	if n := r.XLen(ctx, ingestStream).Val(); n != 1 {
		t.Fatalf("committed backlog was not trimmed: %d", n)
	}
}

func TestPostgresDirectEventWriteHandlesNullAndLiteralUnicodeEscapes(t *testing.T) {
	p, _ := redisFixture(t)
	ctx := context.Background()
	event := gateway.Event{ID: "direct-unicode", Time: time.Now().UTC(), Kind: "warning", RequestID: "direct\x00request", Text: "\x00 \\u0000 \\\x00\n中文🙂" + string([]byte{0xff}), Decision: policy.Decision{Action: policy.Allow}}
	if err := p.insertEvent(ctx, event); err != nil {
		t.Fatalf("direct event write rejected request text: %v", err)
	}
	var stored gateway.Event
	var raw []byte
	if err := p.pool.QueryRow(ctx, "SELECT body FROM audit_events WHERE id='direct-unicode'").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Text != "  \\u0000 \\ \n中文🙂�" || stored.RequestID != "direct request" {
		t.Fatalf("Unicode or literal escapes were corrupted: %+v", stored)
	}
	if !strings.Contains(event.Text, "\x00") {
		t.Fatal("storage cleanup modified the caller's event")
	}
}

func TestTelemetryReplaysOldSpoolWithNullBytesAndKeepsRedaction(t *testing.T) {
	p, r := redisFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	event := gateway.Event{ID: "old-spool", Time: now, Kind: "hit", RequestID: "old-spool", Text: "bad\x00text password=fake-test-secret", Decision: policy.Decision{Action: policy.Block}}
	event.Redact()
	if err := appendSpool(p.spoolPath+".redis", ingestBatch{Producer: "before-hotfix", Sequence: 1, Events: []gateway.Event{event}}); err != nil {
		t.Fatal(err)
	}
	s := &RedisStore{PG: p, redis: r}
	if err := s.replay(ctx); err != nil {
		t.Fatal(err)
	}
	if more, err := s.consume(ctx); err != nil || !more {
		t.Fatalf("old spool could not recover: more=%v error=%v", more, err)
	}
	stored, err := p.Event(ctx, event.ID)
	if err != nil || stored.Text != "bad text password=[已隐藏]" {
		t.Fatalf("spooled event lost content or redaction: text=%q error=%v", stored.Text, err)
	}
	if _, err := os.Stat(p.spoolPath + ".redis.replay"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("completed spool file was not removed", err)
	}
	// The older direct-to-PG spool path must recover through the same storage cleanup.
	event.ID = "direct-old-spool"
	if err := appendSpool(p.spoolPath, event); err != nil {
		t.Fatal(err)
	}
	if err := p.Replay(ctx); err != nil {
		t.Fatal("legacy spool still rejected NUL", err)
	}
	if stored, err := p.Event(ctx, event.ID); err != nil || stored.Text != "bad text password=[已隐藏]" {
		t.Fatal("legacy spool lost event", err)
	}
}

type telemetryReadFailure struct {
	failing atomic.Bool
	calls   atomic.Int64
}

func (*telemetryReadFailure) DialHook(next redis.DialHook) redis.DialHook { return next }
func (*telemetryReadFailure) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *telemetryReadFailure) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "xread" && h.failing.Load() {
			h.calls.Add(1)
			return errors.New("injected persistence failure")
		}
		return next(ctx, cmd)
	}
}

type telemetryTestLog struct {
	mu   sync.Mutex
	text strings.Builder
}

func (l *telemetryTestLog) Write(raw []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.text.Write(raw)
}
func (l *telemetryTestLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.text.String()
}

func TestTelemetryFailuresBackOffLimitLogsAndRecover(t *testing.T) {
	p, r := redisFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := redis.NewClient(r.Options())
	defer client.Close()
	hook := &telemetryReadFailure{}
	hook.failing.Store(true)
	client.AddHook(hook)
	logs := &telemetryTestLog{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	defer slog.SetDefault(previous)
	s := &RedisStore{PG: p, redis: client}
	done := make(chan struct{})
	go func() { defer close(done); s.consumeLoop(ctx) }()
	defer func() { cancel(); <-done }()
	time.Sleep(1250 * time.Millisecond)
	if calls := hook.calls.Load(); calls < 1 || calls > 5 {
		t.Errorf("consumer continuously retried failed persistence: %d attempts", calls)
	}
	if n := strings.Count(logs.String(), "telemetry persistence delayed"); n != 1 {
		t.Errorf("failure logs were not limited: %d", n)
	}
	b := ingestBatch{Producer: "recovery", Sequence: 1, Events: []gateway.Event{{ID: "after-outage", Time: time.Now().UTC(), Kind: "warning", RequestID: "after-outage", Decision: policy.Decision{Action: policy.Allow}}}}
	if err := s.publish(ctx, b); err != nil {
		t.Fatal(err)
	}
	hook.failing.Store(false)
	deadline := time.Now().Add(6 * time.Second)
	for {
		var events int64
		if err := p.pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE id='after-outage'").Scan(&events); err != nil {
			t.Fatal(err)
		}
		if events == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("consumer did not recover after storage became available")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
