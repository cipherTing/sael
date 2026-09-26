package store

import (
	"context"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cipherTing/sael/gateway/internal/gateway"
)

func optionalTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func (s *PG) increment(ctx context.Context, c gateway.Count) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := writeCount(ctx, tx, c); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func durationBand(ms int64) float64 {
	for _, upper := range []int64{1, 2, 5, 10, 20, 50, 100, 150, 200, 300, 500, 750, 1000, 1500, 2000, 3000, 5000, 7500, 10000, 15000, 30000, 60000, 120000, 300000} {
		if ms <= upper {
			return float64(upper)
		}
	}
	return math.Ceil(float64(ms)/300000) * 300000
}

func writeCount(ctx context.Context, tx pgx.Tx, c gateway.Count) error {
	a := newAggregate()
	a.add(c)
	return writeAggregate(ctx, tx, a, nil)
}

const analyticsWhere = ` bucket >= $1 AND bucket < $2
 AND protocol IN ('openai_chat','openai_responses','anthropic','openai_images_generations','openai_images_edits','openai_images_variations')
 AND ($3='' OR protocol=$3) AND ($4='' OR model=$4) `
const analyticsBucket = `CASE
 WHEN $5::bigint=86400 THEN date_trunc('day',bucket,$6::text)
 WHEN $5::bigint=3600 THEN date_trunc('hour',bucket,$6::text)
 ELSE date_bin(make_interval(secs => $5::double precision),bucket,'1970-01-01T00:00:00Z'::timestamptz)
 END`

// Analytics reads aggregate distributions, including clean requests, independently of event retention.
func (s *PG) Analytics(ctx context.Context, f gateway.AnalyticsFilter) (gateway.Analytics, error) {
	out := gateway.Analytics{Since: f.Since, Until: f.Until, StepSeconds: f.StepSeconds(), Traffic: []gateway.TrafficPoint{}, Previous: []gateway.TrafficPoint{}, Distributions: []gateway.DistributionPoint{}, Scenes: []gateway.ScenePoint{}, Errors: []gateway.ErrorPoint{}, Models: []string{}}
	zone := f.Timezone
	if zone == "" {
		zone = "UTC"
	}
	args := []any{f.Since, f.Until, f.Endpoint, f.Model, out.StepSeconds, zone}
	traffic := func(since, until time.Time) ([]gateway.TrafficPoint, error) {
		rows, err := s.pool.Query(ctx, `SELECT `+analyticsBucket+`,protocol,model,outcome,sum(count) FROM gateway_counts_minute WHERE `+analyticsWhere+` GROUP BY 1,2,3,4 ORDER BY 1`, since, until, f.Endpoint, f.Model, out.StepSeconds, zone)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		points := []gateway.TrafficPoint{}
		for rows.Next() {
			var p gateway.TrafficPoint
			if err := rows.Scan(&p.Time, &p.Endpoint, &p.Model, &p.Outcome, &p.Count); err != nil {
				return nil, err
			}
			points = append(points, p)
		}
		return points, rows.Err()
	}
	var err error
	if out.Traffic, err = traffic(f.Since, f.Until); err != nil {
		return out, err
	}
	if out.Previous, err = traffic(f.Since.Add(-f.Until.Sub(f.Since)), f.Since); err != nil {
		return out, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+analyticsBucket+`,protocol,model,metric,upper_bound,sum(count) FROM gateway_measurements_minute WHERE `+analyticsWhere+` GROUP BY 1,2,3,4,5 ORDER BY 1,4,5`, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var p gateway.DistributionPoint
		if err := rows.Scan(&p.Time, &p.Endpoint, &p.Model, &p.Metric, &p.Upper, &p.Count); err != nil {
			rows.Close()
			return out, err
		}
		out.Distributions = append(out.Distributions, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = s.pool.Query(ctx, `SELECT `+analyticsBucket+`,protocol,scene_id,max(scene_name),action,winner_id,max(winner_name),sum(count) FROM gateway_scene_matches_minute WHERE `+analyticsWhere+` GROUP BY 1,2,3,5,6 ORDER BY 1`, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var p gateway.ScenePoint
		if err := rows.Scan(&p.Time, &p.Endpoint, &p.SceneID, &p.Name, &p.Action, &p.WinnerID, &p.WinnerName, &p.Count); err != nil {
			rows.Close()
			return out, err
		}
		out.Scenes = append(out.Scenes, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = s.pool.Query(ctx, `SELECT `+analyticsBucket+`,protocol,kind,sum(count) FROM gateway_jev_errors_minute WHERE `+analyticsWhere+` GROUP BY 1,2,3 ORDER BY 1`, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var p gateway.ErrorPoint
		if err := rows.Scan(&p.Time, &p.Endpoint, &p.Kind, &p.Count); err != nil {
			rows.Close()
			return out, err
		}
		out.Errors = append(out.Errors, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = s.pool.Query(ctx, `SELECT DISTINCT model FROM gateway_counts_minute WHERE bucket >= $1 AND bucket < $2 AND ($3='' OR protocol=$3) AND model<>'' AND protocol IN ('openai_chat','openai_responses','anthropic','openai_images_generations','openai_images_edits','openai_images_variations') ORDER BY model`, f.Since, f.Until, f.Endpoint)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var model string
		if err := rows.Scan(&model); err != nil {
			return out, err
		}
		out.Models = append(out.Models, model)
	}
	return out, rows.Err()
}
