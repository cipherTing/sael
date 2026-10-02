package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis time and one atomic script keep all instances in the same login window.
// Rejected attempts during cooldown do not extend its deadline.
var loginAttemptScript = redis.NewScript(`
local clock=redis.call('TIME')
local now=tonumber(clock[1])*1000+math.floor(tonumber(clock[2])/1000)
local blocked=tonumber(redis.call('HGET',KEYS[1],'blocked') or '0')
if blocked>now then return blocked-now end
local start=tonumber(redis.call('HGET',KEYS[1],'start') or '0')
local count=tonumber(redis.call('HGET',KEYS[1],'count') or '0')
if now-start>=60000 then start=now;count=0 end
count=count+1
if count>3 then
 local strikes=tonumber(redis.call('HGET',KEYS[1],'strikes') or '0')+1
 local delay=math.min(60000*2^math.min(strikes-1,4),900000)
 redis.call('HSET',KEYS[1],'strikes',strikes,'blocked',now+delay,'start',0,'count',0)
 redis.call('PEXPIRE',KEYS[1],3600000)
 return delay
end
redis.call('HSET',KEYS[1],'start',start,'count',count)
redis.call('PEXPIRE',KEYS[1],3600000)
return 0
`)

// LoginAttempt records an attempt and returns any required cooldown.
func (s *RedisStore) LoginAttempt(ctx context.Context, ip string) (time.Duration, error) {
	sum := sha256.Sum256([]byte(ip))
	ms, err := loginAttemptScript.Run(ctx, s.redis, []string{"sael:login:" + hex.EncodeToString(sum[:])}).Int64()
	return time.Duration(ms) * time.Millisecond, err
}

const adminSessionPrefix = "sael:admin_session:"
const adminSessionIndex = "sael:admin_sessions"
const adminSessionLimit = 5

func adminSessionKey(id string) string { return adminSessionPrefix + id }

//nolint:dupword // Consecutive Lua end tokens close nested blocks.
var putAdminSessionScript = redis.NewScript(`
for _,id in ipairs(redis.call('ZRANGE',KEYS[1],0,-1)) do
 if redis.call('EXISTS',ARGV[4]..id)==0 then redis.call('ZREM',KEYS[1],id) end
end
local score=tonumber(ARGV[5])
if ARGV[6]=='adopt' then
 if redis.call('EXISTS',KEYS[2])==0 or redis.call('ZSCORE',KEYS[1],ARGV[1]) then return 0 end
else
 local clock=redis.call('TIME')
 score=tonumber(clock[1])*1000+math.floor(tonumber(clock[2])/1000)
 local latest=redis.call('ZREVRANGE',KEYS[1],0,0,'WITHSCORES')
 if #latest>0 then score=math.max(score,tonumber(latest[2])+1) end
 redis.call('SET',KEYS[2],'1','PX',ARGV[2])
end
local indexTTL=redis.call('PTTL',KEYS[1])
redis.call('ZADD',KEYS[1],score,ARGV[1])
local excess=redis.call('ZCARD',KEYS[1])-tonumber(ARGV[3])
if excess>0 then
 for _,id in ipairs(redis.call('ZRANGE',KEYS[1],0,excess-1)) do
  redis.call('DEL',ARGV[4]..id)
  redis.call('ZREM',KEYS[1],id)
 end
end
redis.call('PEXPIRE',KEYS[1],math.max(indexTTL,tonumber(ARGV[2])))
return 1
`)

// PutAdminSession stores an opaque session identifier with a fixed expiry.
func (s *RedisStore) PutAdminSession(ctx context.Context, token string, ttl time.Duration) error {
	if ttl.Milliseconds() <= 0 {
		return errors.New("admin session expiry must be positive")
	}
	return putAdminSessionScript.Run(ctx, s.redis, []string{adminSessionIndex, adminSessionKey(token)}, token, ttl.Milliseconds(), adminSessionLimit, adminSessionPrefix, 0, "login").Err()
}

// AdminSessionActive checks session validity without extending its lifetime.
func (s *RedisStore) AdminSessionActive(ctx context.Context, token string) (bool, error) {
	n, err := s.redis.Exists(ctx, adminSessionKey(token)).Result()
	return n == 1, err
}

// DeleteAdminSession revokes a session immediately across instances.
func (s *RedisStore) DeleteAdminSession(ctx context.Context, token string) error {
	_, err := s.redis.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Del(ctx, adminSessionKey(token))
		p.ZRem(ctx, adminSessionIndex, token)
		return nil
	})
	return err
}

// Existing fixed-30-day sessions are adopted in expiry order without resetting TTLs.
func (s *RedisStore) migrateAdminSessions(ctx context.Context) error {
	var cursor uint64
	for {
		keys, next, err := s.redis.Scan(ctx, cursor, adminSessionPrefix+"*", 128).Result()
		if err != nil {
			return err
		}
		pipe := s.redis.Pipeline()
		ttls := make([]*redis.DurationCmd, len(keys))
		for i, key := range keys {
			ttls[i] = pipe.PTTL(ctx, key)
		}
		if len(keys) > 0 {
			if _, err := pipe.Exec(ctx); err != nil {
				return err
			}
		}
		for i, key := range keys {
			ttl := ttls[i].Val()
			if ttl <= 0 {
				continue
			}
			issued := time.Now().Add(ttl - 30*24*time.Hour).UnixMilli()
			if err := putAdminSessionScript.Run(ctx, s.redis, []string{adminSessionIndex, key}, strings.TrimPrefix(key, adminSessionPrefix), ttl.Milliseconds(), adminSessionLimit, adminSessionPrefix, issued, "adopt").Err(); err != nil {
				return err
			}
		}
		cursor = next
		if cursor == 0 {
			return nil
		}
	}
}

const trustedKeys = "sael:trusted_keys"

// Only successful credentials have entries. Unknown-key floods cannot grow this set.
// Scores are last activity times, so changing the idle policy takes effect immediately.
//
//nolint:dupword // Consecutive Lua end tokens close nested blocks.
var trustedKeyScript = redis.NewScript(`
local clock=redis.call('TIME')
local now=tonumber(clock[1])*1000+math.floor(tonumber(clock[2])/1000)
local idle=tonumber(ARGV[2])
redis.call('ZREMRANGEBYSCORE',KEYS[1],'-inf',now-idle)
if ARGV[3]=='learn' or (ARGV[3]=='touch' and redis.call('ZSCORE',KEYS[1],ARGV[1])) then
 redis.call('ZADD',KEYS[1],now,ARGV[1])
 redis.call('PEXPIRE',KEYS[1],idle)
 return 1
end
if ARGV[3]=='prune' then
 local latest=redis.call('ZREVRANGE',KEYS[1],0,0,'WITHSCORES')
 if #latest>0 then redis.call('PEXPIRE',KEYS[1],math.max(1,tonumber(latest[2])+idle-now)) end
end
return 0
`)

// TrustedKey validates trust and refreshes activity atomically.
func (s *RedisStore) TrustedKey(ctx context.Context, key string, idle time.Duration) (bool, error) {
	n, err := trustedKeyScript.Run(ctx, s.redis, []string{trustedKeys}, key, idle.Milliseconds(), "touch").Int64()
	return n == 1, err
}

// RememberKey records a credential whose upstream request succeeded.
func (s *RedisStore) RememberKey(ctx context.Context, key string, idle time.Duration) error {
	return trustedKeyScript.Run(ctx, s.redis, []string{trustedKeys}, key, idle.Milliseconds(), "learn").Err()
}

// ForgetKey revokes a credential rejected by its upstream.
func (s *RedisStore) ForgetKey(ctx context.Context, key string) error {
	return s.redis.ZRem(ctx, trustedKeys, key).Err()
}

// PruneTrustedKeys expires inactive credentials using Redis server time.
func (s *RedisStore) PruneTrustedKeys(ctx context.Context, idle time.Duration) error {
	return trustedKeyScript.Run(ctx, s.redis, []string{trustedKeys}, "", idle.Milliseconds(), "prune").Err()
}
