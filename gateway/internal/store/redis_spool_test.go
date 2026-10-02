package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/cipherTing/sael/gateway/internal/gateway"
)

type replayFailureHook struct {
	failSequence int64
	published    []int64
}

func (*replayFailureHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (*replayFailureHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *replayFailureHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "evalsha" || cmd.Name() == "eval" {
			args := cmd.Args()
			if len(args) > 7 && args[3] == ingestStream {
				var batch ingestBatch
				if raw, ok := args[7].([]byte); ok && json.Unmarshal(raw, &batch) == nil {
					if batch.Sequence == h.failSequence {
						return errors.New("injected Redis failure")
					}
					h.published = append(h.published, batch.Sequence)
				}
			}
		}
		return next(ctx, cmd)
	}
}

func TestSpoolReplayResumesAfterRestartWithoutReadingConfirmedPrefix(t *testing.T) {
	p, r := redisFixture(t)
	ctx := context.Background()
	if err := publishBatch.Load(ctx, r).Err(); err != nil {
		t.Fatal(err)
	}
	var data strings.Builder
	for i := int64(1); i <= 6; i++ {
		a := newAggregate()
		a.add(gateway.Count{Time: time.Now(), Protocol: "openai_chat", Outcome: "clean"})
		b, _ := json.Marshal(ingestBatch{Producer: "restart-test", Sequence: i, Rows: a.rows()})
		data.Write(b)
		data.WriteByte('\n')
	}
	if err := os.WriteFile(p.spoolPath+".redis", []byte(data.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := redis.NewClient(r.Options())
	defer broken.Close()
	hook := &replayFailureHook{failSequence: 4}
	broken.AddHook(hook)
	first := &RedisStore{PG: p, redis: broken}
	if err := first.replay(ctx); err == nil {
		t.Fatal("injected failure did not interrupt replay")
	}
	secondClient := redis.NewClient(r.Options())
	defer secondClient.Close()
	seen := &replayFailureHook{}
	secondClient.AddHook(seen)
	second := &RedisStore{PG: p, redis: secondClient}
	if err := second.replay(ctx); err != nil {
		t.Fatal(err)
	}
	for _, seq := range seen.published {
		if seq < 4 {
			t.Fatalf("restart re-read acknowledged batch %d", seq)
		}
	}
	if n := r.XLen(ctx, ingestStream).Val(); n != 6 {
		t.Fatalf("published %d batches, want 6", n)
	}
	for {
		more, err := second.consume(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
	}
	var total int64
	if err := p.pool.QueryRow(ctx, "SELECT sum(count) FROM gateway_counts_minute").Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 6 {
		t.Fatalf("replay lost or duplicated counts: %d", total)
	}
}

func TestCompletedReplayRemovesProducerMarker(t *testing.T) {
	p, r := redisFixture(t)
	ctx := context.Background()
	if err := appendSpool(p.spoolPath+".redis", ingestBatch{Producer: "old-process", Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	s := &RedisStore{PG: p, redis: r}
	if err := s.replay(ctx); err != nil {
		t.Fatal(err)
	}
	if r.HExists(ctx, ingestProducers, "old-process").Val() {
		t.Fatal("completed spool retained an unused producer marker")
	}
}

func TestProducerIdentityAndSequenceSurvivePendingSpoolRestart(t *testing.T) {
	p, r := redisFixture(t)
	ctx := context.Background()
	first := &RedisStore{PG: p, redis: r}
	if err := first.initProducer(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.producerLock.Close() }()
	if err := first.publish(ctx, ingestBatch{Producer: first.producer, Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	if err := appendSpool(p.spoolPath+".redis", ingestBatch{Producer: first.producer, Sequence: 2}); err != nil {
		t.Fatal(err)
	}
	if err := first.cleanupProducerMarkers(ctx, true); err != nil {
		t.Fatal(err)
	}
	if !r.HExists(ctx, ingestProducers, first.producer).Val() {
		t.Fatal("unfinished spool lost its deduplication marker")
	}
	second := &RedisStore{PG: p, redis: r}
	if err := second.initProducer(ctx); err == nil {
		_ = second.producerLock.Close()
		t.Fatal("two publishers acquired the same spool identity")
	}
	if err := first.producerLock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.initProducer(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.producerLock.Close() }()
	if second.producer != first.producer || second.producerSequence != 2 {
		t.Fatalf("restart lost publisher state: first=%s second=%s sequence=%d", first.producer, second.producer, second.producerSequence)
	}
	if err := second.replay(ctx); err != nil {
		t.Fatal(err)
	}
	if err := second.cleanupProducerMarkers(ctx, true); err != nil {
		t.Fatal(err)
	}
	if n := r.XLen(ctx, ingestStream).Val(); n != 2 || r.HExists(ctx, ingestProducers, second.producer).Val() {
		t.Fatalf("recovery lost data or retained finished marker: batches=%d", n)
	}
}
