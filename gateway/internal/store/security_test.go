package store

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cipherTing/sael/gateway/internal/gateway"
	"github.com/redis/go-redis/v9"
)

func TestRedisLoginLimitSharedAcrossInstancesAndBacksOff(t *testing.T) {
	p, r := redisFixture(t)
	ctx := context.Background()
	s := &RedisStore{PG: p, redis: r}
	first, second := gateway.New(s, nil, "secret"), gateway.New(s, nil, "secret")
	defer first.Close()
	defer second.Close()
	attempt := func(server *gateway.Server, want int, retry string) {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/admin/login", strings.NewReader(`{"password":"wrong"}`))
		req.RemoteAddr = "192.0.2.12:1234"
		server.AdminHandler().ServeHTTP(w, req)
		if w.Code != want || w.Header().Get("Retry-After") != retry {
			t.Fatalf("status=%d retry=%s want=%d/%s", w.Code, w.Header().Get("Retry-After"), want, retry)
		}
	}
	attempt(first, 401, "")
	attempt(second, 401, "")
	attempt(first, 401, "")
	attempt(second, 429, "60")
	attempt(first, 429, "60")
	keys, err := r.Keys(ctx, "sael:login:*").Result()
	if err != nil || len(keys) != 1 {
		t.Fatalf("keys=%v err=%v", keys, err)
	}
	// Advance only the cooldown deadline, preserving the strike count.
	if err := r.HSet(ctx, keys[0], "blocked", 0, "start", 0, "count", 0).Err(); err != nil {
		t.Fatal(err)
	}
	attempt(first, 401, "")
	attempt(second, 401, "")
	attempt(first, 401, "")
	attempt(second, 429, "120")
}
func TestTrustedKeyIdleExpiryRenewsAndHonorsChangedPolicy(t *testing.T) {
	p, r := redisFixture(t)
	ctx := context.Background()
	s := &RedisStore{PG: p, redis: r}
	api := gateway.New(s, nil, "secret")
	defer api.Close()
	if api.Security == nil {
		t.Fatal("shared credential store missing")
	}
	security := api.Security
	idle := 30 * 24 * time.Hour
	if err := security.RememberKey(ctx, "fingerprint", idle); err != nil {
		t.Fatal(err)
	}
	key := "sael:trusted_keys"
	now, err := r.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	_ = r.ZAdd(ctx, key, redis.Z{Score: float64(now.Add(-29 * 24 * time.Hour).UnixMilli()), Member: "fingerprint"}).Err()
	if ok, err := security.TrustedKey(ctx, "fingerprint", idle); err != nil || !ok {
		t.Fatal("active credential expired", err)
	}
	last, _ := r.ZScore(ctx, key, "fingerprint").Result()
	if last < float64(now.Add(-time.Second).UnixMilli()) {
		t.Fatal("request did not renew idle time")
	}
	_ = r.ZAdd(ctx, key, redis.Z{Score: float64(now.Add(-8 * 24 * time.Hour).UnixMilli()), Member: "fingerprint"}).Err()
	if ok, err := security.TrustedKey(ctx, "fingerprint", 7*24*time.Hour); err != nil || ok {
		t.Fatal("shorter idle setting was ignored", err)
	}
	if n := r.ZCard(ctx, key).Val(); n != 0 {
		t.Fatal("expired credential retained")
	}
	if ok, err := security.TrustedKey(ctx, "random-input", idle); err != nil || ok {
		t.Fatal("unknown credential trusted", err)
	}
	if n := r.ZCard(ctx, key).Val(); n != 0 {
		t.Fatal("unknown input created cache entries")
	}
}

// These database-only HTTP tests stub admission, which is exercised above with real Redis.
type adminTestSecurity struct{ gateway.SecurityStore }

func (adminTestSecurity) LoginAttempt(context.Context, string) (time.Duration, error) { return 0, nil }

func TestCleanupRemovesIdleKeysWithoutNewClientTraffic(t *testing.T) {
	p, r := redisFixture(t)
	ctx := context.Background()
	s := &RedisStore{PG: p, redis: r}
	now, err := r.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.ZAdd(ctx, trustedKeys, redis.Z{Score: float64(now.Add(-31 * 24 * time.Hour).UnixMilli()), Member: "expired"}, redis.Z{Score: float64(now.Add(-6 * 24 * time.Hour).UnixMilli()), Member: "active"}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneTrustedKeys(ctx, 7*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if n := r.ZCard(ctx, trustedKeys).Val(); n != 1 {
		t.Fatal("idle entries retained", n)
	}
	ttl := r.PTTL(ctx, trustedKeys).Val()
	if ttl < 23*time.Hour || ttl > 24*time.Hour {
		t.Fatal("cleanup renewed inactivity instead of preserving last request", ttl)
	}
}
