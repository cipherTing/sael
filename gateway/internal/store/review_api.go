package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cipherTing/sael/gateway/internal/gateway"
)

func reviewAPISecret() (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "sk-sael-review-" + hex.EncodeToString(raw[:]), nil
}

func reviewAPIKeyView(id, name, note, prefix string, created time.Time, last, revoked *time.Time, count int64) gateway.ReviewAPIKey {
	return gateway.ReviewAPIKey{ID: id, Name: name, Note: note, Prefix: prefix, Masked: prefix + "******", CreatedAt: created, LastUsedAt: last, RevokedAt: revoked, RequestCount: count}
}

// AuthenticateReviewAPIKey verifies a stored secret hash and records its use.
func (s *PG) AuthenticateReviewAPIKey(ctx context.Context, secret string) (gateway.ReviewAPIKey, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return gateway.ReviewAPIKey{}, gateway.ErrInvalidReviewAPIKey
	}
	hash := sha256.Sum256([]byte(secret))
	var key gateway.ReviewAPIKey
	var created time.Time
	var last, revoked *time.Time
	err := s.pool.QueryRow(ctx, `SELECT id,name,note,prefix,created_at,last_used_at,revoked_at FROM review_api_keys WHERE secret_hash=$1 AND revoked_at IS NULL`, hash[:]).Scan(&key.ID, &key.Name, &key.Note, &key.Prefix, &created, &last, &revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return gateway.ReviewAPIKey{}, gateway.ErrInvalidReviewAPIKey
	}
	if err != nil {
		return gateway.ReviewAPIKey{}, err
	}
	key.CreatedAt, key.LastUsedAt, key.RevokedAt = created, last, revoked
	key.Masked = key.Prefix + "******"
	_, err = s.pool.Exec(ctx, "UPDATE review_api_keys SET last_used_at=now() WHERE id=$1", key.ID)
	return key, err
}

// CreateReviewAPIKey persists a hash and returns the new secret exactly once.
func (s *PG) CreateReviewAPIKey(ctx context.Context, name, note string) (gateway.ReviewAPIKey, error) {
	name, note = strings.TrimSpace(name), strings.TrimSpace(note)
	if name == "" || len([]rune(name)) > 80 || len([]rune(note)) > 240 {
		return gateway.ReviewAPIKey{}, fmt.Errorf("审核 API Key 名称或备注无效")
	}
	secret, err := reviewAPISecret()
	if err != nil {
		return gateway.ReviewAPIKey{}, err
	}
	hash := sha256.Sum256([]byte(secret))
	idBytes := make([]byte, 12)
	if _, err := rand.Read(idBytes); err != nil {
		return gateway.ReviewAPIKey{}, err
	}
	id := "rak_" + hex.EncodeToString(idBytes)
	prefix := secret[:min(18, len(secret))]
	var created time.Time
	err = s.pool.QueryRow(ctx, `INSERT INTO review_api_keys(id,name,note,secret_hash,prefix) VALUES($1,$2,$3,$4,$5) RETURNING created_at`, id, postgresText(name), postgresText(note), hash[:], prefix).Scan(&created)
	if err != nil {
		return gateway.ReviewAPIKey{}, err
	}
	view := reviewAPIKeyView(id, name, note, prefix, created, nil, nil, 0)
	view.Secret = secret
	return view, nil
}

// ListReviewAPIKeys returns masked credentials and their request counts.
func (s *PG) ListReviewAPIKeys(ctx context.Context) ([]gateway.ReviewAPIKey, error) {
	rows, err := s.pool.Query(ctx, `SELECT k.id,k.name,k.note,k.prefix,k.created_at,k.last_used_at,k.revoked_at,COALESCE(sum(c.count),0)
FROM review_api_keys k LEFT JOIN review_api_counts_minute c ON c.key_id=k.id
GROUP BY k.id ORDER BY k.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []gateway.ReviewAPIKey{}
	for rows.Next() {
		var id, name, note, prefix string
		var created time.Time
		var last, revoked *time.Time
		var count int64
		if err := rows.Scan(&id, &name, &note, &prefix, &created, &last, &revoked, &count); err != nil {
			return nil, err
		}
		items = append(items, reviewAPIKeyView(id, name, note, prefix, created, last, revoked, count))
	}
	return items, rows.Err()
}

// RevokeReviewAPIKey makes a dedicated review credential unusable.
func (s *PG) RevokeReviewAPIKey(ctx context.Context, id string) error {
	command, err := s.pool.Exec(ctx, "UPDATE review_api_keys SET revoked_at=COALESCE(revoked_at,now()) WHERE id=$1", strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return gateway.ErrNotFound
	}
	return nil
}

func reviewAPIUpperMS(duration int64) int64 {
	for _, upper := range []int64{10, 25, 50, 100, 250, 500, 1000, 2000, 5000, 10000, 30000} {
		if duration <= upper {
			return upper
		}
	}
	return 60000
}

// RecordReviewAPIStat persists one HTTP review measurement transactionally.
func (s *PG) RecordReviewAPIStat(ctx context.Context, stat gateway.ReviewAPIStat) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := writeAggregate(ctx, tx, newAggregate(), nil, []gateway.ReviewAPIStat{stat}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ReviewAPIOverview reads HTTP review statistics independently of gateway ingress.
func (s *PG) ReviewAPIOverview(ctx context.Context, since, until time.Time) (gateway.ReviewAPIOverview, error) {
	out := gateway.ReviewAPIOverview{Since: since, Until: until, Outcomes: []gateway.NamedCount{}, Scenes: []gateway.NamedCount{}, Keys: []gateway.NamedCount{}, Trend: []gateway.ReviewAPITrend{}}
	rows, err := s.pool.Query(ctx, `SELECT outcome,sum(count),sum(cache_hits) FROM review_api_counts_minute WHERE bucket >= $1 AND bucket < $2 GROUP BY outcome ORDER BY outcome`, since, until)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var outcome string
		var count, cache int64
		if err := rows.Scan(&outcome, &count, &cache); err != nil {
			rows.Close()
			return out, err
		}
		out.Requests += count
		out.CacheHits += cache
		switch outcome {
		case "allowed":
			out.Allowed += count
		case "hit":
			out.Hits += count
		case "blocked":
			out.Hits += count
			out.Blocked += count
		case "error":
			out.Errors += count
		}
		out.Outcomes = append(out.Outcomes, gateway.NamedCount{Name: outcome, Count: count})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT coalesce(sum(count),0) FROM review_api_requests_second WHERE second > date_trunc('second',now()) - interval '60 seconds' AND second <= now()`).Scan(&out.RPM); err != nil {
		return out, err
	}
	rows, err = s.pool.Query(ctx, `SELECT bucket,upper_ms,sum(count) FROM review_api_latency_minute WHERE bucket >= $1 AND bucket < $2 GROUP BY bucket,upper_ms ORDER BY bucket,upper_ms`, since, until)
	if err != nil {
		return out, err
	}
	all := map[int64]int64{}
	perMinute := map[time.Time]map[int64]int64{}
	for rows.Next() {
		var bucket time.Time
		var upper, n int64
		if err := rows.Scan(&bucket, &upper, &n); err != nil {
			rows.Close()
			return out, err
		}
		all[upper] += n
		if perMinute[bucket] == nil {
			perMinute[bucket] = map[int64]int64{}
		}
		perMinute[bucket][upper] += n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	out.P50MS, out.P95MS = reviewAPIQuantile(all, 50), reviewAPIQuantile(all, 95)
	rows, err = s.pool.Query(ctx, `SELECT scene_name,sum(count) FROM review_api_scene_counts_minute WHERE bucket >= $1 AND bucket < $2 GROUP BY scene_name ORDER BY 2 DESC,1 LIMIT 20`, since, until)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var item gateway.NamedCount
		if err := rows.Scan(&item.Name, &item.Count); err != nil {
			rows.Close()
			return out, err
		}
		out.Scenes = append(out.Scenes, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	rows, err = s.pool.Query(ctx, `SELECT CASE WHEN c.key_id='' THEN '未认证' ELSE coalesce(k.name,c.key_id)||' · '||coalesce(k.prefix||'******','') END,sum(c.count) FROM review_api_counts_minute c LEFT JOIN review_api_keys k ON k.id=c.key_id WHERE bucket >= $1 AND bucket < $2 GROUP BY c.key_id,k.name,k.prefix ORDER BY 2 DESC,1 LIMIT 20`, since, until)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var item gateway.NamedCount
		if err := rows.Scan(&item.Name, &item.Count); err != nil {
			rows.Close()
			return out, err
		}
		out.Keys = append(out.Keys, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	rows, err = s.pool.Query(ctx, `SELECT bucket,sum(count),coalesce(sum(count) FILTER(WHERE outcome IN ('hit','blocked')),0),coalesce(sum(count) FILTER(WHERE outcome='blocked'),0),coalesce(sum(count) FILTER(WHERE outcome='error'),0),sum(duration_sum_ms)/greatest(sum(duration_samples),1) FROM review_api_counts_minute WHERE bucket >= $1 AND bucket < $2 GROUP BY bucket ORDER BY bucket`, since, until)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var point gateway.ReviewAPITrend
		if err := rows.Scan(&point.Time, &point.Requests, &point.Hits, &point.Blocked, &point.Errors, &point.Duration); err != nil {
			rows.Close()
			return out, err
		}
		point.P50MS, point.P95MS = reviewAPIQuantile(perMinute[point.Time], 50), reviewAPIQuantile(perMinute[point.Time], 95)
		out.Trend = append(out.Trend, point)
	}
	rows.Close()
	return out, rows.Err()
}

func reviewAPIQuantile(buckets map[int64]int64, percentile int64) int64 {
	var total int64
	for _, n := range buckets {
		total += n
	}
	if total == 0 {
		return 0
	}
	target := (total*percentile + 99) / 100
	var seen int64
	for _, upper := range []int64{10, 25, 50, 100, 250, 500, 1000, 2000, 5000, 10000, 30000, 60000} {
		seen += buckets[upper]
		if seen >= target {
			return upper
		}
	}
	return 60000
}
