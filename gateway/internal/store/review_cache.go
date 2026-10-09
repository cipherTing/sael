package store

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/cipherTing/sael/gateway/internal/gateway"
)

// ReviewCache stores disposable condition predicates in a separate Redis instance.
type ReviewCache struct {
	primary *redis.Client
	pg      *PG
	client  *redis.Client
	config  atomic.Pointer[gateway.ReviewCacheConfig]
	ready   atomic.Bool
	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
}

// OpenReviewCache loads saved capacity and starts configuration synchronization.
func OpenReviewCache(ctx context.Context, p *PG, endpoint, primaryEndpoint string) (*ReviewCache, error) {
	opt, err := redis.ParseURL(endpoint)
	if err != nil {
		return nil, errors.New("REVIEW_CACHE_REDIS_URL 无效")
	}
	opt.MaxRetries = -1
	opt.DialTimeout = 100 * time.Millisecond
	opt.ReadTimeout = 100 * time.Millisecond
	opt.WriteTimeout = 100 * time.Millisecond
	opt.ContextTimeoutEnabled = true
	primaryOpt, err := redis.ParseURL(primaryEndpoint)
	if err != nil {
		return nil, errors.New("主 Redis 连接无效")
	}
	primaryOpt.MaxRetries = -1
	primaryOpt.DialTimeout = time.Second
	primaryOpt.ReadTimeout = time.Second
	primaryOpt.ContextTimeoutEnabled = true
	c := &ReviewCache{pg: p, client: redis.NewClient(opt), primary: redis.NewClient(primaryOpt), done: make(chan struct{})}
	cfg, err := c.load(ctx)
	if err != nil {
		_ = c.client.Close()
		_ = c.primary.Close()
		return nil, err
	}
	c.config.Store(&cfg)
	if err := c.apply(ctx, cfg); err != nil {
		slog.Warn("review cache unavailable; using classifier", "error", err)
	}
	work, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	go func() {
		defer close(c.done)
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-work.Done():
				return
			case <-tick.C:
			}
			c.mu.Lock()
			ctx, cancel := context.WithTimeout(work, time.Second)
			cfg, err := c.load(ctx)
			if err == nil {
				c.config.Store(&cfg)
				err = c.apply(ctx, cfg)
			}
			cancel()
			c.mu.Unlock()
			if err != nil {
				c.ready.Store(false)
			}
		}
	}()
	return c, nil
}

// Close stops synchronization and releases both Redis clients.
func (c *ReviewCache) Close() { c.cancel(); <-c.done; _ = c.client.Close(); _ = c.primary.Close() }
func (c *ReviewCache) load(ctx context.Context) (gateway.ReviewCacheConfig, error) {
	var v gateway.ReviewCacheConfig
	err := c.pg.pool.QueryRow(ctx, "SELECT ttl_days,max_bytes FROM gateway_review_cache WHERE id=1").Scan(&v.TTLDays, &v.MaxBytes)
	return v, err
}
func (c *ReviewCache) apply(ctx context.Context, v gateway.ReviewCacheConfig) error {
	applied := false
	defer func() { c.ready.Store(applied) }()
	cacheInfo, err := c.client.Info(ctx, "server").Result()
	if err != nil {
		return err
	}
	primaryInfo, err := c.primary.Info(ctx, "server").Result()
	if err != nil {
		return err
	}
	runID := func(info string) string {
		for _, line := range strings.Split(info, "\r\n") {
			if strings.HasPrefix(line, "run_id:") {
				return strings.TrimPrefix(line, "run_id:")
			}
		}
		return ""
	}
	if runID(cacheInfo) == "" || runID(cacheInfo) == runID(primaryInfo) {
		return errors.New("审核缓存必须使用独立 Redis 实例")
	}

	err = c.client.ConfigSet(ctx, "maxmemory", strconv.FormatInt(v.MaxBytes, 10)).Err()
	if err == nil {
		err = c.client.ConfigSet(ctx, "maxmemory-policy", "allkeys-lru").Err()
	}
	applied = err == nil
	return err
}

// Configure applies capacity and persists settings for subsequent writes and restarts.
func (c *ReviewCache) Configure(ctx context.Context, v gateway.ReviewCacheConfig) error {
	if err := v.Validate(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	tx, err := c.pg.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var old gateway.ReviewCacheConfig
	if err := tx.QueryRow(ctx, "SELECT ttl_days,max_bytes FROM gateway_review_cache WHERE id=1 FOR UPDATE").Scan(&old.TTLDays, &old.MaxBytes); err != nil {
		return err
	}
	if err = c.apply(ctx, v); err != nil {
		return errors.New("审核缓存暂不可用，配置未保存")
	}
	if _, err = tx.Exec(ctx, "UPDATE gateway_review_cache SET ttl_days=$1,max_bytes=$2 WHERE id=1", v.TTLDays, v.MaxBytes); err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		rollback, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = c.apply(rollback, old)
		return err
	}
	c.config.Store(&v)
	return nil
}

// Lookup returns available verdicts without extending their expiry.
func (c *ReviewCache) Lookup(ctx context.Context, keys []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(keys) == 0 {
		return out, nil
	}
	if !c.ready.Load() {
		return nil, errors.New("review cache unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	values, err := c.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	for i, v := range values {
		if v == nil {
			continue
		}
		key := keys[i] // #nosec G602 -- Redis MGET returns exactly one result per requested key.
		switch v {
		case "1":
			out[key] = true
		case "0":
			out[key] = false
		default:
			continue
		}
	}
	return out, nil
}

// Save writes freshly evaluated verdicts with the currently configured TTL.
func (c *ReviewCache) Save(ctx context.Context, values map[string]bool) error {
	if len(values) == 0 {
		return nil
	}
	if !c.ready.Load() {
		return errors.New("review cache unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	ttl := time.Duration(c.config.Load().TTLDays) * 24 * time.Hour
	_, err := c.client.Pipelined(ctx, func(p redis.Pipeliner) error {
		for key, v := range values {
			raw := "0"
			if v {
				raw = "1"
			}
			p.Set(ctx, key, raw, ttl)
		}
		return nil
	})
	return err
}

// Status reports live Redis capacity and persisted request reuse counts.
func (c *ReviewCache) Status(ctx context.Context) (gateway.ReviewCacheStatus, error) {
	cfg, err := c.load(ctx)
	if err != nil {
		return gateway.ReviewCacheStatus{}, err
	}
	out := gateway.ReviewCacheStatus{ReviewCacheConfig: cfg}
	info, err := c.client.Info(ctx, "memory").Result()
	if err == nil {
		out.Available = c.ready.Load()
		for _, line := range strings.Split(info, "\r\n") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				continue
			}
			v, _ := strconv.ParseInt(parts[1], 10, 64)
			if parts[0] == "used_memory" {
				out.UsedBytes = v
			}
			if parts[0] == "maxmemory" {
				out.EffectiveMaxBytes = v
			}
		}
		out.Entries = c.client.DBSize(ctx).Val()
	}
	err = c.pg.pool.QueryRow(ctx, `SELECT coalesce(sum(count) FILTER(WHERE metric='cache_lookup'),0),coalesce(sum(count) FILTER(WHERE metric='cache_hit'),0) FROM gateway_measurements_minute WHERE metric IN ('cache_lookup','cache_hit')`).Scan(&out.Lookups, &out.Hits)
	return out, err
}
