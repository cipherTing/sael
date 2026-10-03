package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cipherTing/sael/gateway/internal/gateway"
	"github.com/cipherTing/sael/gateway/internal/policy"
)

func TestBatchMergesNormalTrafficWithoutRequestDetailsOrScores(t *testing.T) {
	b := newAggregate()
	now := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 100; i++ {
		b.add(gateway.Count{ID: "must-not-persist-id", Time: now.Add(time.Duration(i) * time.Millisecond), Protocol: "openai_chat", Model: "test", Outcome: "clean", ClassifierSample: true, ClassifierMS: 10, JevMS: 5, Scores: []policy.Answer{{Question: "gore", Type: "score", Value: .2}}})
	}
	rows := b.rows()
	var counts, samples, measures int64
	for _, r := range rows {
		if r.Kind == "count" {
			counts += r.Count
			samples += r.Samples
			if r.SumMS != 1000 {
				t.Fatal("wrong duration sum")
			}
		}
		if r.Kind == "measure" {
			measures += r.Count
		}
		if strings.HasPrefix(r.Metric, "hit_score:") {
			t.Fatal("normal scores persisted")
		}
	}
	raw, _ := json.Marshal(rows)
	if counts != 100 || samples != 100 || measures != 200 || len(rows) != 3 || strings.Contains(string(raw), "must-not-persist-id") {
		t.Fatalf("wrong aggregate: %s", raw)
	}
}

func TestBatchKeepsHitScoresAndSceneOverlap(t *testing.T) {
	b := newAggregate()
	b.add(gateway.Count{Time: time.Now().UTC(), Outcome: "blocked", Scores: []policy.Answer{{Question: "gore", Type: "score", Value: 1.51}}, SceneMatches: []gateway.SceneMatch{{SceneID: "winner", WinnerID: "winner", Action: "block"}, {SceneID: "other", WinnerID: "winner", Action: "allow"}}})
	var score, scene int64
	for _, r := range b.rows() {
		if r.Metric == "hit_score:gore" {
			score += r.Count
			if r.Upper != 1.6 {
				t.Fatal("incorrect score bucket")
			}
		}
		if r.Kind == "scene" {
			scene += r.Count
		}
	}
	if score != 1 || scene != 2 {
		t.Fatal("lost hit data")
	}
}

func TestWarningsDoNotBecomeClassifierErrors(t *testing.T) {
	b := newAggregate()
	now := time.Now().UTC()
	for _, c := range []gateway.Count{
		{Time: now, Outcome: "input_too_long", ErrorKind: "classifier_input_too_long"},
		{Time: now, Outcome: "session_blocked", ErrorKind: "session_blocked"},
		{Time: now, Outcome: "unreviewed", ClassifierSample: true, ErrorKind: "classifier_timeout"},
	} {
		b.add(c)
	}
	var requests, samples, errors int64
	for _, row := range b.rows() {
		if row.Kind == "count" {
			requests += row.Count
			samples += row.Samples
		}
		if row.Kind == "error" {
			errors += row.Count
			if row.ErrorKind != "classifier_timeout" {
				t.Fatalf("warning entered Jev error totals: %s", row.ErrorKind)
			}
		}
	}
	if requests != 3 || samples != 1 || errors != 1 {
		t.Fatalf("requests=%d, Jev samples=%d, errors=%d", requests, samples, errors)
	}
}
