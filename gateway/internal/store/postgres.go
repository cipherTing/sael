// Package store persists gateway policy, audit events, and counters in PostgreSQL.
package store

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cipherTing/sael/gateway/internal/gateway"
	"github.com/cipherTing/sael/gateway/internal/policy"
)

//go:embed migrations/*.sql
var migrations embed.FS

// PG persists policy, events, and minute counts in PostgreSQL.
type PG struct {
	pool            *pgxpool.Pool
	spoolPath       string
	spoolMu         sync.Mutex
	policyMu        sync.RWMutex
	cachedPolicy    policy.Policy
	hasCachedPolicy bool
	jevMu           sync.RWMutex
	cachedJev       gateway.JevConfig
	hasCachedJev    bool
}

// Open connects to PostgreSQL and applies the embedded schema.
func Open(ctx context.Context, dsn, spoolPath string) (*PG, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	for _, name := range []string{"migrations/001_init.sql", "migrations/002_jev.sql", "migrations/003_metrics.sql"} {
		sql, err := migrations.ReadFile(name)
		if err != nil {
			pool.Close()
			return nil, err
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			pool.Close()
			return nil, fmt.Errorf("database migration %s: %w", name, err)
		}
	}
	store := &PG{pool: pool, spoolPath: spoolPath}
	if _, err := store.Policy(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if _, err := store.Jev(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

// Close releases PostgreSQL pool resources.
func (s *PG) Close() { s.pool.Close() }

// Jev returns the current saved classifier connection.
func (s *PG) Jev(ctx context.Context) (gateway.JevConfig, error) {
	var c gateway.JevConfig
	err := s.pool.QueryRow(ctx, "SELECT base_url,model,api_key,updated_at FROM gateway_jev WHERE id=1").Scan(&c.BaseURL, &c.Model, &c.APIKey, &c.UpdatedAt)
	if err != nil {
		s.jevMu.RLock()
		defer s.jevMu.RUnlock()
		if s.hasCachedJev {
			return s.cachedJev, nil
		}
		return c, err
	}
	s.jevMu.Lock()
	s.cachedJev, s.hasCachedJev = c, true
	s.jevMu.Unlock()
	return c, err
}

// UpdateJev saves one complete classifier connection.
func (s *PG) UpdateJev(ctx context.Context, c gateway.JevConfig) (gateway.JevConfig, error) {
	err := s.pool.QueryRow(ctx, "UPDATE gateway_jev SET base_url=$1, model=$2, api_key=$3, updated_at=now() WHERE id=1 RETURNING updated_at", c.BaseURL, c.Model, c.APIKey).Scan(&c.UpdatedAt)
	if err == nil {
		s.jevMu.Lock()
		s.cachedJev, s.hasCachedJev = c, true
		s.jevMu.Unlock()
	}
	return c, err
}

// Policy loads the current versioned policy snapshot.
func (s *PG) Policy(ctx context.Context) (policy.Policy, error) {
	var raw []byte
	var result policy.Policy
	err := s.pool.QueryRow(ctx, "SELECT body FROM gateway_policy WHERE id=1").Scan(&raw)
	if err != nil {
		s.policyMu.RLock()
		defer s.policyMu.RUnlock()
		if s.hasCachedPolicy {
			return clonePolicy(s.cachedPolicy), nil
		}
		return result, err
	}
	err = json.Unmarshal(raw, &result)
	if err == nil {
		s.policyMu.Lock()
		s.cachedPolicy, s.hasCachedPolicy = clonePolicy(result), true
		s.policyMu.Unlock()
	}
	return result, err
}

// UpdatePolicy atomically saves a new version and its change record.
func (s *PG) UpdatePolicy(ctx context.Context, expected int64, next policy.Policy, actor string) (policy.Policy, error) {
	if err := policy.Validate(next); err != nil {
		return policy.Policy{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return policy.Policy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var oldRaw []byte
	var version int64
	if err := tx.QueryRow(ctx, "SELECT version, body FROM gateway_policy WHERE id=1 FOR UPDATE").Scan(&version, &oldRaw); err != nil {
		return policy.Policy{}, err
	}
	if version != expected {
		return policy.Policy{}, gateway.ErrConflict
	}
	next.Version = version + 1
	newRaw, err := json.Marshal(next)
	if err != nil {
		return policy.Policy{}, err
	}
	if _, err := tx.Exec(ctx, "UPDATE gateway_policy SET version=$1, body=$2, updated_at=now() WHERE id=1", next.Version, newRaw); err != nil {
		return policy.Policy{}, err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO policy_changes(version,actor,before,after) VALUES ($1,$2,$3,$4)", next.Version, actor, oldRaw, newRaw); err != nil {
		return policy.Policy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return policy.Policy{}, err
	}
	s.policyMu.Lock()
	s.cachedPolicy, s.hasCachedPolicy = clonePolicy(next), true
	s.policyMu.Unlock()
	return next, nil
}

func clonePolicy(p policy.Policy) policy.Policy {
	out := p
	out.Thresholds = make(map[string]float64, len(p.Thresholds))
	for key, value := range p.Thresholds {
		out.Thresholds[key] = value
	}
	out.Scenes = make([]policy.Scene, len(p.Scenes))
	for i, scene := range p.Scenes {
		out.Scenes[i] = scene
		out.Scenes[i].Questions = append([]string(nil), scene.Questions...)
	}
	if p.PreviewChars != nil {
		value := *p.PreviewChars
		out.PreviewChars = &value
	}
	if p.RetentionDays != nil {
		value := *p.RetentionDays
		out.RetentionDays = &value
	}
	return out
}

func (s *PG) insertEvent(ctx context.Context, event gateway.Event) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO audit_events(id,time,kind,action,request_id,body) VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (id) DO NOTHING`, event.ID, event.Time, event.Kind, event.Decision.Action, event.RequestID, raw)
	return err
}

// WriteEvent persists an event or appends it to the local spool on DB failure.
func (s *PG) WriteEvent(ctx context.Context, event gateway.Event) error {
	if err := s.insertEvent(ctx, event); err == nil {
		return nil
	}
	if s.spoolPath == "" {
		return errors.New("database event write failed and spool is not configured")
	}
	s.spoolMu.Lock()
	defer s.spoolMu.Unlock()
	return appendSpool(s.spoolPath, event)
}

func appendSpool(path string, item any) error {
	// #nosec G703 -- The spool path is an operator deployment setting, never request data.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// #nosec G703 -- The spool path is an operator deployment setting, never request data.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(f).Encode(item); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Replay imports spooled events and counts after PostgreSQL recovers.
func (s *PG) Replay(ctx context.Context) error {
	if s.spoolPath == "" {
		return nil
	}
	s.spoolMu.Lock()
	defer s.spoolMu.Unlock()
	if err := s.replayEvents(ctx); err != nil {
		return err
	}
	return s.replayCounts(ctx)
}

func (s *PG) replayEvents(ctx context.Context) error {
	// #nosec G703 -- The spool path is an operator deployment setting, never request data.
	f, err := os.Open(s.spoolPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var event gateway.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return err
		}
		if err := s.insertEvent(ctx, event); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// #nosec G703 -- The spool path is an operator deployment setting, never request data.
	return os.Remove(s.spoolPath)
}

const incrementSQL = `INSERT INTO gateway_counts_minute(bucket,protocol,model,outcome,count,last_seen,classifier_sum_ms,classifier_samples,upstream_errors)
		VALUES (date_trunc('minute',$1::timestamptz),$2,$3,$4,1,$1,$5,CASE WHEN $6::boolean THEN 1 ELSE 0 END,CASE WHEN $7::boolean THEN 1 ELSE 0 END)
		ON CONFLICT (bucket,protocol,model,outcome) DO UPDATE SET count=gateway_counts_minute.count+1,last_seen=EXCLUDED.last_seen,
		classifier_sum_ms=gateway_counts_minute.classifier_sum_ms+EXCLUDED.classifier_sum_ms,
		classifier_samples=gateway_counts_minute.classifier_samples+EXCLUDED.classifier_samples,
		upstream_errors=gateway_counts_minute.upstream_errors+EXCLUDED.upstream_errors`

// Increment adds one request outcome to its minute bucket or local spool.
func (s *PG) Increment(ctx context.Context, count gateway.Count) error {
	_, err := s.pool.Exec(ctx, incrementSQL, count.Time, count.Protocol, count.Model, count.Outcome, count.ClassifierMS, count.ClassifierSample, count.UpstreamError)
	if err == nil {
		return nil
	}
	if s.spoolPath == "" || count.ID == "" {
		return err
	}
	s.spoolMu.Lock()
	defer s.spoolMu.Unlock()
	return appendSpool(s.spoolPath+".counts", count)
}

func (s *PG) replayCounts(ctx context.Context) error {
	path := s.spoolPath + ".counts"
	// #nosec G703 -- The spool path is an operator deployment setting, never request data.
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var count gateway.Count
		if err := json.Unmarshal(scanner.Bytes(), &count); err != nil {
			return err
		}
		if count.ID == "" {
			return errors.New("spooled count is missing ID")
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, "INSERT INTO replayed_counts(id) VALUES ($1) ON CONFLICT DO NOTHING", count.ID)
		if err == nil && tag.RowsAffected() == 1 {
			_, err = tx.Exec(ctx, incrementSQL, count.Time, count.Protocol, count.Model, count.Outcome, count.ClassifierMS, count.ClassifierSample, count.UpstreamError)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// #nosec G703 -- The spool path is an operator deployment setting, never request data.
	return os.Remove(path)
}

// Overview aggregates minute outcomes and event breakdowns.
func (s *PG) Overview(ctx context.Context, since time.Time) (gateway.Overview, error) {
	out := gateway.Overview{Since: since, Trend: []gateway.TrendPoint{}, Scenes: []gateway.NamedCount{}, Questions: []gateway.NamedCount{}}
	rows, err := s.pool.Query(ctx, `SELECT bucket,outcome,sum(count),sum(classifier_sum_ms),sum(classifier_samples),sum(upstream_errors) FROM gateway_counts_minute WHERE bucket >= date_trunc('minute',$1::timestamptz) GROUP BY bucket,outcome ORDER BY bucket`, since)
	if err != nil {
		return out, err
	}
	var classifierSum, classifierSamples int64
	for rows.Next() {
		var point gateway.TrendPoint
		if err := rows.Scan(&point.Time, &point.Outcome, &point.Count, &point.ClassifierSumMS, &point.ClassifierSamples, &point.UpstreamErrors); err != nil {
			rows.Close()
			return out, err
		}
		out.Trend = append(out.Trend, point)
		out.Total += point.Count
		out.UpstreamErrors += point.UpstreamErrors
		classifierSum += point.ClassifierSumMS
		classifierSamples += point.ClassifierSamples
		switch point.Outcome {
		case "clean":
			out.Checked += point.Count
		case "hit_allowed":
			out.Checked += point.Count
			out.Hits += point.Count
		case "blocked":
			out.Checked += point.Count
			out.Hits += point.Count
			out.Blocked += point.Count
		case "unreviewed":
			out.Unreviewed += point.Count
		case "no_text":
			out.NoText += point.Count
		case "disabled":
			out.Disabled += point.Count
		}
	}
	if classifierSamples > 0 {
		out.ClassifierAvgMS = classifierSum / classifierSamples
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	var updatedAt *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT max(last_seen) FROM gateway_counts_minute WHERE bucket >= date_trunc('minute',$1::timestamptz)`, since).Scan(&updatedAt); err != nil {
		return out, err
	}
	if updatedAt != nil {
		out.UpdatedAt = *updatedAt
	}
	rows, err = s.pool.Query(ctx, `SELECT coalesce(nullif(body->'decision'->>'scene_name',''),nullif(body->'decision'->>'scene_id',''),'unmatched'),count(*) FROM audit_events WHERE kind='hit' AND time >= $1 GROUP BY 1 ORDER BY 2 DESC`, since)
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
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = s.pool.Query(ctx, `SELECT hit->>'question',count(*) FROM audit_events CROSS JOIN LATERAL jsonb_array_elements(body->'decision'->'hits') hit WHERE kind='hit' AND time >= $1 GROUP BY 1 ORDER BY 2 DESC`, since)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var item gateway.NamedCount
		if err := rows.Scan(&item.Name, &item.Count); err != nil {
			rows.Close()
			return out, err
		}
		out.Questions = append(out.Questions, item)
	}
	err = rows.Err()
	rows.Close()
	return out, err
}

// Events returns a filtered page of hit and failure events.
func (s *PG) Events(ctx context.Context, f gateway.EventFilter) ([]gateway.Event, error) {
	if f.Limit < 1 || f.Limit > 100 {
		f.Limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT body FROM audit_events WHERE time >= $1
		AND ($2='' OR kind=$2) AND ($3='' OR action=$3)
		AND ($4='' OR request_id ILIKE '%'||$4||'%' OR body->>'text_preview' ILIKE '%'||$4||'%' OR body->>'model' ILIKE '%'||$4||'%')
		ORDER BY time DESC LIMIT $5 OFFSET $6`, f.Since, f.Kind, f.Action, f.Search, f.Limit, f.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gateway.Event{}
	for rows.Next() {
		var raw []byte
		var event gateway.Event
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

// Event retrieves one stored event by ID.
func (s *PG) Event(ctx context.Context, id string) (gateway.Event, error) {
	var raw []byte
	var out gateway.Event
	err := s.pool.QueryRow(ctx, "SELECT body FROM audit_events WHERE id=$1", id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, gateway.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}

// Changes returns recent policy edits in reverse order.
func (s *PG) Changes(ctx context.Context) ([]gateway.PolicyChange, error) {
	rows, err := s.pool.Query(ctx, "SELECT time,version,actor,before,after FROM policy_changes ORDER BY id DESC LIMIT 20")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gateway.PolicyChange{}
	for rows.Next() {
		var change gateway.PolicyChange
		var before, after []byte
		if err := rows.Scan(&change.Time, &change.Version, &change.Actor, &before, &after); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(before, &change.Before); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(after, &change.After); err != nil {
			return nil, err
		}
		out = append(out, change)
	}
	return out, rows.Err()
}

// Prune removes events older than the configured retention period.
func (s *PG) Prune(ctx context.Context, days int) error {
	if days < 1 {
		return nil
	}
	_, err := s.pool.Exec(ctx, "DELETE FROM audit_events WHERE time < now() - ($1 * interval '1 day')", days)
	return err
}
