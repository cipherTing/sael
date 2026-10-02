package store

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/cipherTing/sael/gateway/internal/gateway"
)

func TestAdminLoginKeepsFiveSessionsAcrossInstances(t *testing.T) {
	p, r := redisFixture(t)
	s := &RedisStore{PG: p, redis: r}
	servers := []*gateway.Server{gateway.New(s, nil, "secret"), gateway.New(s, nil, "secret")}
	defer servers[0].Close()
	defer servers[1].Close()
	cookies := []*http.Cookie{}
	for i := 0; i < 12; i++ {
		req := httptest.NewRequest("POST", "/admin/login", strings.NewReader(`{"password":"secret"}`))
		req.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", i+1)
		w := httptest.NewRecorder()
		servers[i%2].AdminHandler().ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("login %d: status=%d", i, w.Code)
		}
		cookies = append(cookies, w.Result().Cookies()[0])
	}
	for i, cookie := range cookies {
		req := httptest.NewRequest("GET", "/admin/session", http.NoBody)
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		servers[1].AdminHandler().ServeHTTP(w, req)
		want := 401
		if i >= 7 {
			want = 200
		}
		if w.Code != want {
			t.Errorf("login %d remained %d, want %d", i, w.Code, want)
		}
	}
	if n := len(r.Keys(context.Background(), "sael:admin_session:*").Val()); n != 5 {
		t.Fatalf("retained %d session keys", n)
	}
}

func TestExistingAdminSessionsAreCappedAtStartup(t *testing.T) {
	p, r := redisFixture(t)
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		if err := r.Set(ctx, adminSessionKey(fmt.Sprintf("old-%02d", i)), "1", 30*24*time.Hour-time.Duration(12-i)*time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
	}
	s, err := OpenRedis(ctx, p, os.Getenv("TEST_REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 12; i++ {
		active, err := s.AdminSessionActive(ctx, fmt.Sprintf("old-%02d", i))
		if err != nil {
			t.Fatal(err)
		}
		if active != (i >= 7) {
			t.Errorf("legacy login %d active=%v", i, active)
		}
	}
}

func TestRedisAdminSessionSurvivesServerReplacementAndExpires(t *testing.T) {
	p, r := redisFixture(t)
	ctx := context.Background()
	s := &RedisStore{PG: p, redis: r}
	first := gateway.New(s, nil, "secret")
	second := gateway.New(s, nil, "secret")
	defer first.Close()
	defer second.Close()
	login := httptest.NewRecorder()
	first.AdminHandler().ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(`{"password":"secret"}`)))
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	keys, err := r.Keys(ctx, "sael:admin_session:*").Result()
	if err != nil || len(keys) != 1 || strings.Contains(keys[0], cookie.Value) {
		t.Fatalf("session key leaked cookie or was not saved: count=%d err=%v", len(keys), err)
	}
	if ttl := r.TTL(ctx, keys[0]).Val(); ttl < 30*24*time.Hour-2*time.Second || ttl > 30*24*time.Hour {
		t.Fatalf("Redis session TTL = %s", ttl)
	}
	check := func(server *gateway.Server) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/admin/session", http.NoBody)
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		server.AdminHandler().ServeHTTP(w, req)
		return w.Code
	}
	if status := check(second); status != http.StatusOK {
		t.Fatalf("session lost across server instances: %d", status)
	}
	if err := r.Del(ctx, keys[0]).Err(); err != nil {
		t.Fatal(err)
	}
	if status := check(first); status != http.StatusUnauthorized {
		t.Fatalf("expired session remained valid: %d", status)
	}
}

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
type adminTestSecurity struct {
	gateway.SecurityStore
	sessions map[string]time.Time
}

func (*adminTestSecurity) LoginAttempt(context.Context, string) (time.Duration, error) { return 0, nil }
func (s *adminTestSecurity) PutAdminSession(_ context.Context, id string, ttl time.Duration) error {
	if s.sessions == nil {
		s.sessions = make(map[string]time.Time)
	}
	s.sessions[id] = time.Now().Add(ttl)
	return nil
}
func (s *adminTestSecurity) AdminSessionActive(_ context.Context, id string) (bool, error) {
	return time.Now().Before(s.sessions[id]), nil
}
func (s *adminTestSecurity) DeleteAdminSession(_ context.Context, id string) error {
	delete(s.sessions, id)
	return nil
}

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
