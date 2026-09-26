package store

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/cipherTing/sael/gateway/internal/gateway"
)

func streamAfter(a, b string) bool {
	aa, bb := strings.SplitN(a, "-", 2), strings.SplitN(b, "-", 2)
	for i := 0; i < 2; i++ {
		x, _ := strconv.ParseUint(aa[i], 10, 64)
		y, _ := strconv.ParseUint(bb[i], 10, 64)
		if x != y {
			return x > y
		}
	}
	return false
}
func (s *PG) cursor(ctx context.Context) (string, error) {
	if _, err := s.pool.Exec(ctx, "INSERT INTO gateway_ingest_cursor(name,last_id) VALUES($1,'0-0') ON CONFLICT DO NOTHING", ingestStream); err != nil {
		return "", err
	}
	var id string
	err := s.pool.QueryRow(ctx, "SELECT last_id FROM gateway_ingest_cursor WHERE name=$1", ingestStream).Scan(&id)
	return id, err
}
func (s *PG) applyStream(ctx context.Context, messages []redis.XMessage) error {
	if _, err := s.cursor(ctx); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var last string
	if err := tx.QueryRow(ctx, "SELECT last_id FROM gateway_ingest_cursor WHERE name=$1 FOR UPDATE", ingestStream).Scan(&last); err != nil {
		return err
	}
	a := newAggregate()
	var events []gateway.Event
	for _, m := range messages {
		if !streamAfter(m.ID, last) {
			continue
		}
		raw, ok := m.Values["data"].(string)
		if !ok {
			return errors.New("invalid telemetry batch")
		}
		var b ingestBatch
		if err := json.Unmarshal([]byte(raw), &b); err != nil {
			return err
		}
		for _, r := range b.Rows {
			a.merge(r)
		}
		events = append(events, b.Events...)
		last = m.ID
	}
	if err := writeAggregate(ctx, tx, a, events); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE gateway_ingest_cursor SET last_id=$2 WHERE name=$1", ingestStream, last); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *RedisStore) consume(ctx context.Context) (bool, error) {
	last, err := s.cursor(ctx)
	if err != nil {
		return false, err
	}
	streams, err := s.redis.XRead(ctx, &redis.XReadArgs{Streams: []string{ingestStream, last}, Count: 32, Block: -1}).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	messages := streams[0].Messages
	if err := s.applyStream(ctx, messages); err != nil {
		return false, err
	}
	// Never trim by length: an outage must retain all uncommitted aggregates and events.
	last = messages[len(messages)-1].ID
	if err := s.redis.XTrimMinID(ctx, ingestStream, last).Err(); err != nil {
		return false, err
	}
	return true, nil
}
func (s *RedisStore) consumeLoop(ctx context.Context) {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	var replayed time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		work, cancel := context.WithTimeout(ctx, 3*time.Second)
		if time.Since(replayed) > time.Second {
			if err := s.replay(work); err != nil && ctx.Err() == nil {
				slog.Warn("Redis spool replay failed", "error", err)
			}
			replayed = time.Now()
		}
		for i := 0; i < 8; i++ {
			more, err := s.consume(work)
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn("telemetry persistence delayed", "error", err)
				}
				break
			}
			if !more {
				break
			}
		}
		cancel()
	}
}
