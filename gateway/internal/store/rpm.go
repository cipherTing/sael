package store

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/cipherTing/sael/gateway/internal/gateway"
)

const rpmPrefix = "sael:rpm:"

type rpmPoint struct {
	Second   int64  `json:"second"`
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"`
	Count    int64  `json:"count"`
}

// ObserveIngress queues the rate sample before classification or forwarding can delay it.
func (s *RedisStore) ObserveIngress(ctx context.Context, c gateway.Count) error {
	return s.enqueue(ctx, ingestItem{rpm: &rpmPoint{Second: c.Time.Unix(), Endpoint: c.Protocol, Model: c.Model, Count: 1}})
}
func collectRPM(items []ingestItem) []rpmPoint {
	counts := make(map[rpmPoint]int64)
	for _, item := range items {
		if item.rpm != nil {
			key := *item.rpm
			key.Count = 0
			counts[key]++
		}
	}
	out := make([]rpmPoint, 0, len(counts))
	for key, n := range counts {
		key.Count = n
		out = append(out, key)
	}
	return out
}
func (s *RedisStore) recentRPM(ctx context.Context, now time.Time, endpoint, model string) (int64, error) {
	pipe := s.redis.Pipeline()
	commands := make([]*redis.MapStringStringCmd, 0, 60)
	// Sixty complete seconds give a rolling minute without mixing partial/current buckets.
	for second := now.Unix() - 60; second < now.Unix(); second++ {
		commands = append(commands, pipe.HGetAll(ctx, rpmPrefix+strconv.FormatInt(second, 10)))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	var total int64
	for _, cmd := range commands {
		for field, value := range cmd.Val() {
			var scope [2]string
			if err := json.Unmarshal([]byte(field), &scope); err != nil {
				return 0, err
			}
			if (endpoint == "" || scope[0] == endpoint) && (model == "" || scope[1] == model) {
				n, err := strconv.ParseInt(value, 10, 64)
				if err != nil {
					return 0, err
				}
				total += n
			}
		}
	}
	return total, nil
}

// Analytics combines stored history with the live ingress rate for the same endpoint/model.
func (s *RedisStore) Analytics(ctx context.Context, f gateway.AnalyticsFilter) (gateway.Analytics, error) {
	out, err := s.PG.Analytics(ctx, f)
	if err != nil {
		return out, err
	}
	n, err := s.recentRPM(ctx, time.Now(), f.Endpoint, f.Model)
	if err != nil {
		slog.Warn("live RPM unavailable", "error", err)
		return out, nil
	}
	out.CurrentRPM = &n
	return out, nil
}
