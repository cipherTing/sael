package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func (s *RedisStore) LoginAttempt(ctx context.Context, ip string) (time.Duration, error) {
	sum := sha256.Sum256([]byte(ip))
	ms, err := loginAttemptScript.Run(ctx, s.redis, []string{"sael:login:" + hex.EncodeToString(sum[:])}).Int64()
	return time.Duration(ms) * time.Millisecond, err
}

const trustedKeys = "sael:trusted_keys"

// Only successful credentials have entries. Unknown-key floods cannot grow this set.
// Scores are last activity times, so changing the idle policy takes effect immediately.
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

func (s *RedisStore) TrustedKey(ctx context.Context, key string, idle time.Duration) (bool, error) {
	n, err := trustedKeyScript.Run(ctx, s.redis, []string{trustedKeys}, key, idle.Milliseconds(), "touch").Int64()
	return n == 1, err
}
func (s *RedisStore) RememberKey(ctx context.Context, key string, idle time.Duration) error {
	return trustedKeyScript.Run(ctx, s.redis, []string{trustedKeys}, key, idle.Milliseconds(), "learn").Err()
}
func (s *RedisStore) ForgetKey(ctx context.Context, key string) error {
	return s.redis.ZRem(ctx, trustedKeys, key).Err()
}

func (s *RedisStore) PruneTrustedKeys(ctx context.Context, idle time.Duration) error {
	return trustedKeyScript.Run(ctx, s.redis, []string{trustedKeys}, "", idle.Milliseconds(), "prune").Err()
}
