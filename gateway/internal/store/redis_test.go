package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/cipherTing/sael/gateway/internal/gateway"
)

func TestEnqueueReturnsWhenCallerDeadlineExpires(t *testing.T) {
	s := &RedisStore{queue: make(chan ingestItem, 1), stopCh: make(chan struct{})}
	s.queue <- ingestItem{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Increment(ctx, gateway.Count{}) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		close(s.stopCh)
		<-done
		t.Fatal("full telemetry queue ignored the caller deadline")
	}
}

func TestCancelledTelemetryDoesNotEnterQueue(t *testing.T) {
	s := &RedisStore{queue: make(chan ingestItem, 1), stopCh: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Increment(ctx, gateway.Count{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if len(s.queue) != 0 {
		t.Fatal("cancelled telemetry was accepted")
	}
}

func TestRedisSessionScopeCoversLaterFreeze(t *testing.T) {
	p, r := redisFixture(t)
	s := &RedisStore{PG: p, redis: r}
	ctx := context.Background()
	early, late := time.Now().Add(time.Minute), time.Now().Add(2*time.Minute)
	for _, until := range []time.Time{early, late, early} {
		if err := s.PutSessionBlock(ctx, "scope:shared", until); err != nil {
			t.Fatal(err)
		}
	}
	if ttl := r.PTTL(ctx, sessionPrefix+"scope:shared").Val(); ttl < 119*time.Second {
		t.Fatalf("scope expires before the later conversation: %s", ttl)
	}
	if err := s.PutSessionBlock(ctx, "exact", early); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSessionBlock(ctx, "exact", late); err != nil {
		t.Fatal(err)
	}
	if ttl := r.PTTL(ctx, sessionPrefix+"exact").Val(); ttl > time.Minute {
		t.Fatal("exact conversation freeze was renewed")
	}
}

type sessionLookupHook struct {
	batches []int
	failAt  int
}

func (*sessionLookupHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (*sessionLookupHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *sessionLookupHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "mget" {
			h.batches = append(h.batches, len(cmd.Args())-1)
			if len(h.batches) == h.failAt {
				return errors.New("injected second-batch failure")
			}
		}
		return next(ctx, cmd)
	}
}

func TestSessionLookupCrossesRedisBatchesInCandidateOrder(t *testing.T) {
	p, r := redisFixture(t)
	ctx := context.Background()
	keys := make([]string, 256)
	for i := range keys {
		keys[i] = fmt.Sprintf("candidate-%03d", i)
	}
	for _, index := range []int{150, 200} {
		if err := r.Set(ctx, sessionPrefix+keys[index], "1", time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
	}
	// An expired earlier candidate must not win over active blocks in batch two.
	if err := r.Set(ctx, sessionPrefix+keys[1], "1", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if err := r.PExpire(ctx, sessionPrefix+keys[1], -time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(r.Options())
	defer client.Close()
	hook := &sessionLookupHook{}
	client.AddHook(hook)
	s := &RedisStore{PG: p, redis: client}
	key, err := s.FindSessionBlock(ctx, keys, time.Now())
	if err != nil || key != "candidate-150" {
		t.Fatalf("first active match = %q, error=%v", key, err)
	}
	if len(hook.batches) != 2 || hook.batches[0] != 128 || hook.batches[1] != 128 {
		t.Fatalf("unexpected Redis batch sizes: %v", hook.batches)
	}
	hook.batches, hook.failAt = nil, 2
	if key, err := s.FindSessionBlock(ctx, keys, time.Now()); key != "" || err == nil {
		t.Fatalf("failed second batch returned a decision: key=%q error=%v", key, err)
	}
}

func TestPostgresSessionScopeUsesLatestExpiryAndFindsFirstCandidate(t *testing.T) {
	p, _ := redisFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, until := range []time.Time{now.Add(time.Minute), now.Add(2 * time.Minute), now.Add(time.Minute)} {
		if err := p.PutSessionBlock(ctx, "scope:shared", until); err != nil {
			t.Fatal(err)
		}
	}
	active, err := p.SessionBlockActive(ctx, "scope:shared", now.Add(90*time.Second))
	if err != nil || !active {
		t.Fatalf("scope expired ahead of its later frozen conversation: active=%v error=%v", active, err)
	}
	for _, key := range []string{"later", "first"} {
		if err := p.PutSessionBlock(ctx, key, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := p.FindSessionBlock(ctx, []string{"missing", "first", "later"}, now); err != nil || got != "first" {
		t.Fatalf("candidate order lost: key=%q error=%v", got, err)
	}
}

func TestCompletedProducerMarkersAreCleanedAfterRestart(t *testing.T) {
	p, r := redisFixture(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		s, err := OpenRedis(ctx, p, os.Getenv("TEST_REDIS_URL"))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Increment(ctx, gateway.Count{Time: time.Now(), Protocol: "openai_chat", Outcome: "clean"}); err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
	if n := r.HLen(ctx, ingestProducers).Val(); n != 0 {
		t.Fatalf("finished processes left %d deduplication markers", n)
	}
}

func redisFixture(t *testing.T) (*PG, *redis.Client) {
	t.Helper()
	dsn, endpoint := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_REDIS_URL")
	if dsn == "" || endpoint == "" {
		t.Skip("TEST_DATABASE_URL and TEST_REDIS_URL required (isolated test databases)")
	}
	p, err := Open(context.Background(), dsn, filepath.Join(t.TempDir(), "spool"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	opt, err := redis.ParseURL(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	r := redis.NewClient(opt)
	t.Cleanup(func() { _ = r.Close() })
	if err := r.FlushDB(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.pool.Exec(context.Background(), "TRUNCATE gateway_ingest_cursor,gateway_counts_minute,gateway_measurements_minute,gateway_scene_matches_minute,gateway_jev_errors_minute,audit_events,gateway_session_blocks"); err != nil {
		t.Fatal(err)
	}
	return p, r
}
func TestRedisPublishAndDatabaseReplayAreIdempotent(t *testing.T) {
	p, r := redisFixture(t)
	ctx := context.Background()
	a := newAggregate()
	for i := 0; i < 100; i++ {
		a.add(gateway.Count{Time: time.Now(), Protocol: "openai_chat", Outcome: "clean"})
	}
	s := &RedisStore{PG: p, redis: r}
	b := ingestBatch{Producer: "test-producer", Sequence: 1, Rows: a.rows()}
	if err := s.publish(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := s.publish(ctx, b); err != nil {
		t.Fatal(err)
	}
	msgs, err := r.XRange(ctx, ingestStream, "-", "+").Result()
	if err != nil || len(msgs) != 1 {
		t.Fatalf("duplicate publish: %d %v", len(msgs), err)
	}
	for i := 0; i < 2; i++ {
		if err := p.applyStream(ctx, msgs); err != nil {
			t.Fatal(err)
		}
	}
	var total int64
	_ = p.pool.QueryRow(ctx, "SELECT sum(count) FROM gateway_counts_minute").Scan(&total)
	if total != 100 {
		t.Fatalf("replay changed total: %d", total)
	}
}
func TestRedisFailureSpoolsOnlyRedactedBatchesAndReplaysOnce(t *testing.T) {
	p, r := redisFixture(t)
	ctx := context.Background()
	offline := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 20 * time.Millisecond})
	defer offline.Close()
	s := &RedisStore{PG: p, redis: offline}
	event := gateway.Event{ID: "event-redacted", RequestID: "problem", Time: time.Now(), Kind: "hit", Text: "password=private-secret user@example.com"}
	s.queue = make(chan ingestItem, 8)
	s.producerDone = make(chan struct{})
	go s.produce()
	if err := s.WriteEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	close(s.queue)
	<-s.producerDone
	raw, err := os.ReadFile(p.spoolPath + ".redis")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-secret") || strings.Contains(string(raw), "user@example.com") {
		t.Fatal("spool leaked prompt")
	}
	s.redis = r
	if err := s.replay(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.replay(ctx); err != nil {
		t.Fatal(err)
	}
	msgs, _ := r.XRange(ctx, ingestStream, "-", "+").Result()
	if len(msgs) != 1 {
		t.Fatalf("replayed %d batches", len(msgs))
	}
	var got ingestBatch
	if err := json.Unmarshal([]byte(msgs[0].Values["data"].(string)), &got); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Events[0].Text, "private-secret") {
		t.Fatal("redis leaked prompt")
	}
}

func TestCloseStopsProducerWhenRedisAndSpoolAreBothUnavailable(t *testing.T) {
	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "spool-parent")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	offline := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		MaxRetries:   -1,
		DialTimeout:  20 * time.Millisecond,
		ReadTimeout:  20 * time.Millisecond,
		WriteTimeout: 20 * time.Millisecond,
	})
	defer offline.Close()
	s := &RedisStore{
		PG:           &PG{spoolPath: filepath.Join(blocker, "telemetry")},
		redis:        offline,
		queue:        make(chan ingestItem, 1),
		stopCh:       make(chan struct{}),
		producerDone: make(chan struct{}),
	}
	s.producerCtx, s.producerCancel = context.WithCancel(context.Background())
	go s.produce()
	if err := s.Increment(context.Background(), gateway.Count{Time: time.Now(), Protocol: "openai_chat", Outcome: "clean"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		s.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(telemetryShutdownTimeout + 2*time.Second):
		t.Fatal("Close blocked while telemetry backends were unavailable")
	}
}

func TestCloseUnblocksEnqueueWaitingOnFullQueue(t *testing.T) {
	offline := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	s := &RedisStore{
		queue:        make(chan ingestItem, 1),
		stopCh:       make(chan struct{}),
		producerDone: make(chan struct{}),
		redis:        offline,
	}
	defer offline.Close()
	close(s.producerDone)
	s.queue <- ingestItem{}

	enqueued := make(chan error, 1)
	go func() {
		enqueued <- s.Increment(context.Background(), gateway.Count{Time: time.Now(), Protocol: "openai_chat", Outcome: "clean"})
	}()
	time.Sleep(20 * time.Millisecond)

	closed := make(chan struct{})
	go func() {
		s.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close blocked behind a full telemetry queue")
	}
	select {
	case err := <-enqueued:
		if err == nil {
			t.Fatal("enqueue succeeded after telemetry store closed")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked enqueue did not observe shutdown")
	}
}
func TestRedisSessionMigrationExpiryAndRetry(t *testing.T) {
	p, r := redisFixture(t)
	ctx := context.Background()
	until := time.Now().Add(time.Minute)
	if err := p.PutSessionBlock(ctx, "old", until); err != nil {
		t.Fatal(err)
	}
	s := &RedisStore{PG: p, redis: r}
	if err := s.migrateSessionBlocks(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SessionBlockActive(ctx, "old", time.Now()); !ok || err != nil {
		t.Fatal("active freeze lost", err)
	}
	before := r.PTTL(ctx, sessionPrefix+"old").Val()
	if err := s.PutSessionBlock(ctx, "old", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if r.PTTL(ctx, sessionPrefix+"old").Val() > before {
		t.Fatal("retry extended freeze")
	}
	if err := s.PutSessionBlock(ctx, "expired", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.SessionBlockActive(ctx, "expired", time.Now()); ok {
		t.Fatal("expired freeze active")
	}
}

func TestRuntimeKeepsSnapshotsAndSynchronizesConfigAcrossInstances(t *testing.T) {
	p, _ := redisFixture(t)
	ctx := context.Background()
	first, err := OpenRedis(ctx, p, os.Getenv("TEST_REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	secondPG, err := Open(ctx, os.Getenv("TEST_DATABASE_URL"), filepath.Join(t.TempDir(), "spool"))
	if err != nil {
		t.Fatal(err)
	}
	defer secondPG.Close()
	second, err := OpenRedis(ctx, secondPG, os.Getenv("TEST_REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := first.UpdateUpstream(ctx, gateway.UpstreamConfig{BaseURL: "https://sync.example"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		u, _ := second.Upstream(ctx)
		if u.BaseURL == "https://sync.example" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second instance never refreshed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	u, err := second.Upstream(canceled)
	if err != nil || u.BaseURL != "https://sync.example" {
		t.Fatal("request path performed configuration I/O")
	}
	for i := 0; i < 50; i++ {
		if err := first.Increment(ctx, gateway.Count{Time: time.Now(), Protocol: "openai_chat", Outcome: "clean"}); err != nil {
			t.Fatal(err)
		}
	}
	deadline = time.Now().Add(3 * time.Second)
	for {
		var n int64
		_ = p.pool.QueryRow(ctx, "SELECT coalesce(sum(count),0) FROM gateway_counts_minute").Scan(&n)
		if n == 50 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("batched traffic lost: %d", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestConsumerRollsBackCountsAndCursorWhenEventWriteFails(t *testing.T) {
	p, r := redisFixture(t)
	ctx := context.Background()
	_, err := p.pool.Exec(ctx, `CREATE OR REPLACE FUNCTION reject_load_event() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'test write failure'; END$$; CREATE TRIGGER reject_load_event BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_load_event()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = p.pool.Exec(ctx, "DROP TRIGGER IF EXISTS reject_load_event ON audit_events; DROP FUNCTION IF EXISTS reject_load_event()")
	})
	a := newAggregate()
	a.add(gateway.Count{Time: time.Now(), Protocol: "openai_chat", Outcome: "blocked"})
	s := &RedisStore{PG: p, redis: r}
	if err := s.publish(ctx, ingestBatch{Producer: "rollback", Sequence: 1, Rows: a.rows(), Events: []gateway.Event{{ID: "rollback-event", Time: time.Now(), Kind: "hit"}}}); err != nil {
		t.Fatal(err)
	}
	msgs, _ := r.XRange(ctx, ingestStream, "-", "+").Result()
	if err := p.applyStream(ctx, msgs); err == nil {
		t.Fatal("database rejection ignored")
	}
	var n int64
	_ = p.pool.QueryRow(ctx, "SELECT count(*) FROM gateway_counts_minute").Scan(&n)
	cursor, err := p.cursor(ctx)
	if err != nil || cursor != "0-0" || n != 0 {
		t.Fatal("partial batch committed")
	}
	if _, err := p.pool.Exec(ctx, "DROP TRIGGER reject_load_event ON audit_events"); err != nil {
		t.Fatal(err)
	}
	if err := p.applyStream(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	_ = p.pool.QueryRow(ctx, "SELECT sum(count) FROM gateway_counts_minute").Scan(&n)
	if n != 1 {
		t.Fatal("recovery lost or duplicated count")
	}
}

func TestTelemetryReturnsAfterEnqueueAndCloseDrainsAcceptedRecords(t *testing.T) {
	p, r := redisFixture(t)
	ctx := context.Background()
	s, err := OpenRedis(ctx, p, os.Getenv("TEST_REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	// Hold the persistence boundary. Requests must still enqueue without waiting for I/O.
	s.spoolMu.Lock()
	done := make(chan error, 1)
	go func() {
		done <- s.Increment(ctx, gateway.Count{Time: time.Now(), Protocol: "openai_chat", Outcome: "clean"})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("request waited for telemetry persistence")
	}
	s.spoolMu.Unlock()
	s.Close()
	messages, err := r.XRange(ctx, ingestStream, "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("graceful close did not drain: %d batches", len(messages))
	}
	var b ingestBatch
	if err := json.Unmarshal([]byte(messages[0].Values["data"].(string)), &b); err != nil {
		t.Fatal(err)
	}
	if len(b.Rows) != 1 || b.Rows[0].Count != 1 {
		t.Fatal("accepted aggregate lost on close")
	}
}

func TestRecentRPMUsesRollingMinuteFiltersAndIdempotentPublishing(t *testing.T) {
	p, r := redisFixture(t)
	s := &RedisStore{PG: p, redis: r}
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	batch := ingestBatch{Producer: "rpm-test", Sequence: 1, RPM: []rpmPoint{
		{Second: now.Add(-30 * time.Second).Unix(), Endpoint: "openai_chat", Model: "a", Count: 10},
		{Second: now.Add(-time.Second).Unix(), Endpoint: "openai_responses", Model: "b", Count: 7},
		{Second: now.Add(-61 * time.Second).Unix(), Endpoint: "openai_chat", Model: "a", Count: 999},
	}}
	for i := 0; i < 2; i++ {
		if err := s.publish(ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		endpoint, model string
		want            int64
	}{{"", "", 17}, {"openai_chat", "", 10}, {"", "b", 7}, {"anthropic", "", 0}} {
		n, err := s.recentRPM(ctx, now, tc.endpoint, tc.model)
		if err != nil || n != tc.want {
			t.Fatalf("wrong rolling RPM: %d want %d, %v", n, tc.want, err)
		}
	}
	if n, err := s.recentRPM(ctx, now.Add(time.Minute), "", ""); err != nil || n != 0 {
		t.Fatal("old traffic remained in current RPM")
	}
}
