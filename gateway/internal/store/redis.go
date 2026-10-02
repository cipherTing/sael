package store

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"github.com/cipherTing/sael/gateway/internal/gateway"
	"github.com/cipherTing/sael/gateway/internal/policy"
)

const ingestStream = "sael:ingest"
const ingestProducers = "sael:ingest:producers"
const configChannel = "sael:config:changed"
const sessionPrefix = "sael:session:"

const telemetryShutdownTimeout = 5 * time.Second

type ingestBatch struct {
	RPM      []rpmPoint      `json:"rpm,omitempty"`
	Producer string          `json:"producer"`
	Sequence int64           `json:"sequence"`
	Rows     []aggregateRow  `json:"rows,omitempty"`
	Events   []gateway.Event `json:"events,omitempty"`
}
type configSnapshot struct {
	policy   policy.Policy
	jev      gateway.JevConfig
	upstream gateway.UpstreamConfig
}
type ingestItem struct {
	rpm   *rpmPoint
	count *gateway.Count
	event *gateway.Event
}

// RedisStore serves configuration from memory and durably batches telemetry in Redis.
// PostgreSQL remains authoritative for configuration and historical aggregates.
type RedisStore struct {
	credentialWrites singleflight.Group
	*PG
	redis            *redis.Client
	snapshot         atomic.Pointer[configSnapshot]
	refreshMu        sync.Mutex
	queue            chan ingestItem
	stopCh           chan struct{}
	accepting        sync.RWMutex
	enqueues         sync.WaitGroup
	closed           bool
	producerDone     chan struct{}
	producerCtx      context.Context
	producerCancel   context.CancelFunc
	cancel           context.CancelFunc
	workers          sync.WaitGroup
	spoolMu          sync.Mutex
	spooling         bool
	replayMu         sync.Mutex
	producer         string
	producerSequence int64
	producerLock     *os.File
	producerDrained  bool
	retired          map[string]bool
}

// OpenRedis verifies both stores before the listeners accept requests.
func OpenRedis(ctx context.Context, p *PG, endpoint string) (*RedisStore, error) {
	opt, err := redis.ParseURL(endpoint)
	if err != nil {
		return nil, errors.New("invalid REDIS_URL")
	}
	opt.MaxRetries = -1
	opt.DialTimeout = time.Second
	opt.ReadTimeout = time.Second
	opt.WriteTimeout = time.Second
	opt.ContextTimeoutEnabled = true
	r := redis.NewClient(opt)
	s := &RedisStore{PG: p, redis: r, queue: make(chan ingestItem, 8192), stopCh: make(chan struct{}), producerDone: make(chan struct{})}
	fail := func(err error) (*RedisStore, error) {
		if s.producerLock != nil {
			_ = s.producerLock.Close()
		}
		_ = r.Close()
		return nil, err
	}
	if err := r.Ping(ctx).Err(); err != nil {
		return fail(err)
	}
	if err := s.refresh(ctx); err != nil {
		return fail(err)
	}
	if err := s.migrateSessionBlocks(ctx); err != nil {
		return fail(err)
	}
	if err := s.migrateAdminSessions(ctx); err != nil {
		return fail(err)
	}
	for _, path := range []string{p.spoolPath + ".redis", p.spoolPath + ".redis.replay"} {
		if _, err := os.Stat(path); err == nil { // #nosec G703 -- Fixed spool filenames from the operator-configured data directory.
			s.spooling = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return fail(err)
		}
	}
	if err := s.initProducer(ctx); err != nil {
		return fail(err)
	}
	work, cancel := context.WithCancel(context.Background())
	producerCtx, producerCancel := context.WithCancel(context.Background())
	s.producerCtx = producerCtx
	s.producerCancel = producerCancel
	s.cancel = cancel
	// #nosec G118 -- Close drains telemetry after HTTP shutdown; the startup context must not cancel accepted records.
	go s.produce()
	s.workers.Add(2)
	go func() { defer s.workers.Done(); s.synchronize(work) }()
	go func() { defer s.workers.Done(); s.consumeLoop(work) }()
	return s, nil
}

// Close drains accepted records before stopping background workers. It does not close PG.
func (s *RedisStore) Close() {
	s.accepting.Lock()
	if s.closed {
		s.accepting.Unlock()
		return
	}
	s.closed = true
	if s.stopCh == nil {
		s.stopCh = make(chan struct{})
	}
	close(s.stopCh)
	s.accepting.Unlock()
	s.enqueues.Wait()
	if s.queue != nil {
		close(s.queue)
	}
	producerStopped := false
	timer := time.NewTimer(telemetryShutdownTimeout)
	select {
	case <-s.producerDone:
		producerStopped = true
	case <-timer.C:
		if s.producerCancel != nil {
			s.producerCancel()
		}
		select {
		case <-s.producerDone:
			producerStopped = true
		case <-time.After(time.Second):
			slog.Error("telemetry producer did not stop before shutdown deadline")
		}
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	if !producerStopped && s.producerCancel != nil {
		s.producerCancel()
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.workers.Wait()
	if producerStopped && s.PG != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := s.cleanupProducerMarkers(ctx, s.producerDrained); err != nil {
			slog.Warn("statistics producer cleanup failed", "error", err)
		}
		cancel()
	}
	if s.producerLock != nil {
		_ = s.producerLock.Close()
	}
	_ = s.redis.Close()
}
func (s *RedisStore) enqueue(ctx context.Context, item ingestItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.accepting.Lock()
	if s.stopCh == nil {
		s.stopCh = make(chan struct{})
	}
	if s.closed {
		s.accepting.Unlock()
		return errors.New("telemetry store closed")
	}
	queue, stop := s.queue, s.stopCh
	s.enqueues.Add(1)
	s.accepting.Unlock()
	defer s.enqueues.Done()
	// A full queue applies backpressure until shutdown signals stop. Do not hold
	// the lifecycle lock while waiting, or Close could never cancel this send.
	select {
	case queue <- item:
		return nil
	case <-stop:
		return errors.New("telemetry store closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Increment queues one request for aggregation without waiting for network or disk I/O.
func (s *RedisStore) Increment(ctx context.Context, c gateway.Count) error {
	return s.enqueue(ctx, ingestItem{count: &c})
}

// WriteEvent redacts a problem request before placing it in the bounded queue.
func (s *RedisStore) WriteEvent(ctx context.Context, e gateway.Event) error {
	e.Redact()
	return s.enqueue(ctx, ingestItem{event: &e})
}

func (s *RedisStore) produce() {
	defer close(s.producerDone)
	producerCtx := s.producerCtx
	if producerCtx == nil {
		producerCtx = context.Background()
	}
	producer := s.producer
	if producer == "" {
		producer = rand.Text()
		s.producer = producer
	}
	sequence := s.producerSequence
	for {
		var first ingestItem
		select {
		case item, ok := <-s.queue:
			if !ok {
				s.producerDrained = true
				return
			}
			first = item
		case <-producerCtx.Done():
			return
		}
		items := []ingestItem{first}
		timer := time.NewTimer(2 * time.Millisecond)
	collect:
		for len(items) < 128 {
			select {
			case item, ok := <-s.queue:
				if !ok {
					break collect
				}
				items = append(items, item)
			case <-timer.C:
				break collect
			case <-producerCtx.Done():
				timer.Stop()
				return
			}
		}
		timer.Stop()
		a := newAggregate()
		var events []gateway.Event
		for _, item := range items {
			if item.count != nil {
				a.add(*item.count)
			}
			if item.event != nil {
				events = append(events, *item.event)
			}
		}
		sequence++
		batch := ingestBatch{Producer: producer, Sequence: sequence, Rows: a.rows(), Events: events, RPM: collectRPM(items)}
		for {
			ctx, cancel := context.WithTimeout(producerCtx, 250*time.Millisecond)
			err := s.persist(ctx, batch)
			cancel()
			if err == nil {
				break
			}
			slog.Error("telemetry persistence failed; retaining batch and applying backpressure", "error", err)
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-producerCtx.Done():
				if !errors.Is(producerCtx.Err(), context.Canceled) {
					slog.Warn("telemetry producer stopped before persisting batch", "error", producerCtx.Err())
				}
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-timer.C:
			}
		}
	}
}

// One sequence per producer makes a lost publish reply safe to retry, without a key per request.
// Producers publish in sequence, including local-spool replay.
var publishBatch = redis.NewScript(`
local last=tonumber(redis.call('HGET',KEYS[2],ARGV[1]) or '0')
if last >= tonumber(ARGV[2]) then return 0 end
redis.call('XADD',KEYS[1],'*','data',ARGV[3])
for _,p in ipairs(cjson.decode(ARGV[4])) do
 local key=ARGV[5]..string.format('%.0f',p.second)
 redis.call('HINCRBY',key,cjson.encode({p.endpoint,p.model}),p.count)
 redis.call('EXPIREAT',key,p.second+120)
end
redis.call('HSET',KEYS[2],ARGV[1],ARGV[2])
return 1`)

func (s *RedisStore) publish(ctx context.Context, b ingestBatch) error {
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	points := []byte("[]")
	if len(b.RPM) > 0 {
		points, err = json.Marshal(b.RPM)
		if err != nil {
			return err
		}
	}
	return publishBatch.Run(ctx, s.redis, []string{ingestStream, ingestProducers}, b.Producer, b.Sequence, raw, points, rpmPrefix).Err()
}

// Policy returns an independent copy of the last saved policy snapshot.
func (s *RedisStore) Policy(context.Context) (policy.Policy, error) {
	return clonePolicy(s.snapshot.Load().policy), nil
}

// Jev returns the cached classifier connection.
func (s *RedisStore) Jev(context.Context) (gateway.JevConfig, error) {
	return s.snapshot.Load().jev, nil
}

// Upstream returns the cached forwarding destination.
func (s *RedisStore) Upstream(context.Context) (gateway.UpstreamConfig, error) {
	return s.snapshot.Load().upstream, nil
}
func (s *RedisStore) refresh(ctx context.Context) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	p, err := s.PG.Policy(ctx)
	if err != nil {
		return err
	}
	j, err := s.PG.Jev(ctx)
	if err != nil {
		return err
	}
	u, err := s.PG.Upstream(ctx)
	if err != nil {
		return err
	}
	s.snapshot.Store(&configSnapshot{p, j, u})
	return nil
}
func (s *RedisStore) changed(ctx context.Context) {
	if err := s.refresh(ctx); err != nil {
		slog.Error("refresh saved configuration", "error", err)
	}
	if err := s.redis.Publish(ctx, configChannel, "changed").Err(); err != nil {
		slog.Warn("configuration notification failed; periodic refresh will retry", "error", err)
	}
}

// UpdatePolicy persists a policy and refreshes the local and remote snapshots.
func (s *RedisStore) UpdatePolicy(ctx context.Context, update gateway.PolicyUpdate, actor string) (policy.Policy, error) {
	p, err := s.PG.UpdatePolicy(ctx, update, actor)
	if err == nil {
		s.changed(ctx)
	}
	return p, err
}

// UpdateJev persists the classifier connection and notifies other instances.
func (s *RedisStore) UpdateJev(ctx context.Context, c gateway.JevConfig) (gateway.JevConfig, error) {
	c, err := s.PG.UpdateJev(ctx, c)
	if err == nil {
		s.changed(ctx)
	}
	return c, err
}

// UpdateUpstream persists the destination and notifies other instances.
func (s *RedisStore) UpdateUpstream(ctx context.Context, c gateway.UpstreamConfig) (gateway.UpstreamConfig, error) {
	c, err := s.PG.UpdateUpstream(ctx, c)
	if err == nil {
		s.changed(ctx)
	}
	return c, err
}
func (s *RedisStore) synchronize(ctx context.Context) {
	sub := s.redis.Subscribe(ctx, configChannel)
	defer func() { _ = sub.Close() }()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	// Pub/Sub only accelerates updates; reconciliation also repairs missed/reconnect notifications.
	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.Channel():
		case <-ticker.C:
		}
		work, cancel := context.WithTimeout(ctx, time.Second)
		if err := s.refresh(work); err != nil && ctx.Err() == nil {
			slog.Warn("configuration refresh failed", "error", err)
		}
		cancel()
	}
}

// PutSessionBlock freezes a hashed session without extending an existing expiry.
func (s *RedisStore) PutSessionBlock(ctx context.Context, key string, until time.Time) error {
	if !until.After(time.Now()) {
		return nil
	}
	if strings.HasPrefix(key, "scope:") {
		return extendSessionScope.Run(ctx, s.redis, []string{sessionPrefix + key}, until.UnixMilli()).Err()
	}
	// SET NX preserves the original expiry when concurrent requests block the same session.
	err := s.redis.Do(ctx, "SET", sessionPrefix+key, "1", "NX", "PXAT", until.UnixMilli()).Err()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	return err
}

// A shared scope must outlive every exact conversation it guards. Exact keys use NX.
var extendSessionScope = redis.NewScript(`
local expires=redis.call('PEXPIRETIME',KEYS[1])
if expires==-2 then redis.call('SET',KEYS[1],'1','PXAT',ARGV[1])
elseif expires>=0 and expires<tonumber(ARGV[1]) then redis.call('PEXPIREAT',KEYS[1],ARGV[1]) end
return 1`)

// SessionBlockActive checks the Redis expiry for a hashed session.
func (s *RedisStore) SessionBlockActive(ctx context.Context, key string, _ time.Time) (bool, error) {
	n, err := s.redis.Exists(ctx, sessionPrefix+key).Result()
	return n > 0, err
}

// FindSessionBlock follows sub2api's bounded MGET batches and earliest-match order.
func (s *RedisStore) FindSessionBlock(ctx context.Context, keys []string, _ time.Time) (string, error) {
	const batchSize = 128
	for start := 0; start < len(keys); start += batchSize {
		end := min(start+batchSize, len(keys))
		redisKeys := make([]string, end-start)
		for i, key := range keys[start:end] {
			redisKeys[i] = sessionPrefix + key
		}
		values, err := s.redis.MGet(ctx, redisKeys...).Result()
		if err != nil {
			return "", err
		}
		for i, value := range values {
			if value != nil {
				return keys[start+i], nil
			}
		}
	}
	return "", nil
}
func (s *RedisStore) migrateSessionBlocks(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, "SELECT session_hash,expires_at FROM gateway_session_blocks WHERE expires_at>now()")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var until time.Time
		if err := rows.Scan(&key, &until); err != nil {
			return err
		}
		if err := s.PutSessionBlock(ctx, key, until); err != nil && !errors.Is(err, redis.Nil) {
			return err
		}
	}
	return rows.Err()
}
