package store

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cipherTing/sael/gateway/internal/gateway"
)

type aggregateKey struct {
	Kind      string    `json:"kind"`
	Bucket    time.Time `json:"bucket"`
	Protocol  string    `json:"protocol"`
	Model     string    `json:"model"`
	Outcome   string    `json:"outcome"`
	Metric    string    `json:"metric"`
	Upper     float64   `json:"upper_bound"`
	SceneID   string    `json:"scene_id"`
	Action    string    `json:"action"`
	WinnerID  string    `json:"winner_id"`
	ErrorKind string    `json:"error_kind"`
}
type aggregateRow struct {
	aggregateKey
	Count      int64     `json:"count"`
	SumMS      int64     `json:"sum_ms"`
	Samples    int64     `json:"samples"`
	LastSeen   time.Time `json:"last_seen"`
	Name       string    `json:"scene_name"`
	WinnerName string    `json:"winner_name"`
}
type aggregate map[aggregateKey]aggregateRow

func newAggregate() aggregate { return make(aggregate) }
func (a aggregate) merge(r aggregateRow) {
	old := a[r.aggregateKey]
	r.Count += old.Count
	r.SumMS += old.SumMS
	r.Samples += old.Samples
	if old.LastSeen.After(r.LastSeen) {
		r.LastSeen = old.LastSeen
	}
	a[r.aggregateKey] = r
}
func (a aggregate) add(c gateway.Count) {
	key := aggregateKey{Kind: "count", Bucket: c.Time.UTC().Truncate(time.Minute), Protocol: c.Protocol, Model: c.Model, Outcome: c.Outcome}
	row := aggregateRow{aggregateKey: key, Count: 1, LastSeen: c.Time}
	if c.ClassifierSample {
		row.Samples = 1
		row.SumMS = c.ClassifierMS
	}
	a.merge(row)
	key.Outcome = ""
	measurement := func(metric string, upper float64) {
		k := key
		k.Kind = "measure"
		k.Metric = metric
		k.Upper = upper
		a.merge(aggregateRow{aggregateKey: k, Count: 1})
	}
	if c.ClassifierSample {
		measurement("review_ms", durationBand(c.ClassifierMS))
		measurement("jev_ms", durationBand(c.JevMS))
	}
	if c.Outcome == "blocked" || c.Outcome == "hit_allowed" {
		for _, s := range c.Scores {
			step := .05
			if s.Type == "score" {
				step = .1
			}
			measurement("hit_score:"+s.Question, math.Round(math.Max(step, math.Ceil(s.Value/step-1e-9)*step)*100)/100)
		}
		for _, s := range c.SceneMatches {
			k := key
			k.Kind = "scene"
			k.SceneID = s.SceneID
			k.Action = s.Action
			k.WinnerID = s.WinnerID
			a.merge(aggregateRow{aggregateKey: k, Count: 1, Name: s.Name, WinnerName: s.WinnerName})
		}
	}
	if c.ErrorKind != "" {
		k := key
		k.Kind = "error"
		k.ErrorKind = c.ErrorKind
		a.merge(aggregateRow{aggregateKey: k, Count: 1})
	}
}
func (a aggregate) rows() []aggregateRow {
	out := make([]aggregateRow, 0, len(a))
	for _, r := range a {
		out = append(out, r)
	}
	return out
}

const aggregateRecords = ` FROM jsonb_to_recordset($1::jsonb) AS x(kind text,bucket timestamptz,protocol text,model text,outcome text,metric text,upper_bound double precision,scene_id text,action text,winner_id text,error_kind text,count bigint,sum_ms bigint,samples bigint,last_seen timestamptz,scene_name text,winner_name text) `

var aggregateSQL = []string{
	`INSERT INTO gateway_counts_minute(bucket,protocol,model,outcome,count,classifier_sum_ms,classifier_samples,last_seen)
 SELECT bucket,protocol,model,outcome,count,sum_ms,samples,last_seen` + aggregateRecords + `WHERE kind='count'
 ON CONFLICT(bucket,protocol,model,outcome) DO UPDATE SET count=gateway_counts_minute.count+EXCLUDED.count,classifier_sum_ms=gateway_counts_minute.classifier_sum_ms+EXCLUDED.classifier_sum_ms,classifier_samples=gateway_counts_minute.classifier_samples+EXCLUDED.classifier_samples,last_seen=GREATEST(gateway_counts_minute.last_seen,EXCLUDED.last_seen)`,
	`INSERT INTO gateway_measurements_minute(bucket,protocol,model,metric,upper_bound,count)
 SELECT bucket,protocol,model,metric,upper_bound,count` + aggregateRecords + `WHERE kind='measure'
 ON CONFLICT(bucket,protocol,model,metric,upper_bound) DO UPDATE SET count=gateway_measurements_minute.count+EXCLUDED.count`,
	`INSERT INTO gateway_scene_matches_minute(bucket,protocol,model,scene_id,scene_name,action,winner_id,winner_name,count)
 SELECT bucket,protocol,model,scene_id,scene_name,action,winner_id,winner_name,count` + aggregateRecords + `WHERE kind='scene'
 ON CONFLICT(bucket,protocol,model,scene_id,action,winner_id) DO UPDATE SET count=gateway_scene_matches_minute.count+EXCLUDED.count,scene_name=EXCLUDED.scene_name,winner_name=EXCLUDED.winner_name`,
	`INSERT INTO gateway_jev_errors_minute(bucket,protocol,model,kind,count)
 SELECT bucket,protocol,model,error_kind,count` + aggregateRecords + `WHERE kind='error'
 ON CONFLICT(bucket,protocol,model,kind) DO UPDATE SET count=gateway_jev_errors_minute.count+EXCLUDED.count`,
}

func writeAggregate(ctx context.Context, tx pgx.Tx, a aggregate, events []gateway.Event) error {
	batch := &pgx.Batch{}
	if len(a) > 0 {
		raw, err := json.Marshal(a.rows())
		if err != nil {
			return err
		}
		for _, sql := range aggregateSQL {
			batch.Queue(sql, raw)
		}
	}
	if len(events) > 0 {
		raw, err := json.Marshal(events)
		if err != nil {
			return err
		}
		batch.Queue(`INSERT INTO audit_events(id,time,kind,action,request_id,body)
   SELECT e->>'id',(e->>'time')::timestamptz,e->>'kind',e->'decision'->>'action',e->>'request_id',e
   FROM jsonb_array_elements($1::jsonb) e ON CONFLICT(id) DO NOTHING`, raw)
	}
	return tx.SendBatch(ctx, batch).Close()
}
