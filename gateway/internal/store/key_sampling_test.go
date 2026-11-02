package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/cipherTing/sael/gateway/internal/gateway"
	"github.com/cipherTing/sael/gateway/internal/policy"
)

func TestKeySamplingDefaultsKnownKeysTo100(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := Open(ctx, dsn, filepath.Join(t.TempDir(), "spool"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.InitCredentials(ctx, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))); err != nil {
		t.Fatal(err)
	}
	secret := "known-client-secret"
	knownID := gateway.CredentialFingerprint("https://relay.example", secret)
	if err := p.saveCredential(ctx, knownID, "https://relay.example", secret); err != nil {
		t.Fatal(err)
	}
	if rate, err := p.KeySampleRate(ctx, knownID); err != nil || rate != 100 {
		t.Fatalf("unspecified sample rate=%d err=%v, want 100", rate, err)
	}
	if err := p.SetKeySampleRate(ctx, knownID, 37); err != nil {
		t.Fatal(err)
	}
	if rate, err := p.KeySampleRate(ctx, knownID); err != nil || rate != 37 {
		t.Fatalf("updated sample rate=%d err=%v, want 37", rate, err)
	}
	if err := p.SetKeySampleRate(ctx, knownID, 0); err != nil {
		t.Fatal(err)
	}
	if rate, err := p.KeySampleRate(ctx, knownID); err != nil || rate != 0 {
		t.Fatalf("zero sample rate=%d err=%v, want 0", rate, err)
	}
	if err := p.SetKeySampleRate(ctx, knownID, 101); err == nil {
		t.Fatal("sample rate above 100 was accepted")
	}
	now := time.Now().UTC()
	eventPrefix := strconv.FormatInt(now.UnixNano(), 10)
	events := []gateway.Event{
		{ID: "sampling-exempt-" + eventPrefix, Time: now, Kind: "exempt", CredentialID: knownID, Text: "private prompt", Decision: policy.Decision{Action: policy.Allow, Reason: "key_exempt"}},
		{ID: "sampling-skipped-" + eventPrefix, Time: now.Add(-time.Second), Kind: "sampled", CredentialID: knownID, Text: "another prompt", Decision: policy.Decision{Action: policy.Allow, Reason: "sampling_skipped"}},
		{ID: "sampling-hit-" + eventPrefix, Time: now.Add(-2 * time.Second), Kind: "hit", Decision: policy.Decision{Action: policy.Block}},
	}
	for _, event := range events {
		if err := p.WriteEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	defaultRows, err := p.Events(ctx, gateway.EventFilter{Since: now.Add(-time.Minute), Kind: "hit", Limit: 50})
	if err != nil || len(defaultRows) != 1 || defaultRows[0].ID != events[2].ID {
		t.Fatalf("default records include unreviewed event: %+v %v", defaultRows, err)
	}
	shownRows, err := p.Events(ctx, gateway.EventFilter{Since: now.Add(-time.Minute), Kind: "hit", IncludeUnreviewed: true, Limit: 50})
	if err != nil || len(shownRows) != 3 {
		t.Fatalf("opt-in records omit unreviewed events: %+v %v", shownRows, err)
	}
	stored, err := p.Event(ctx, events[1].ID)
	if err != nil || stored.Text != "another prompt" || stored.Kind != "sampled" {
		t.Fatalf("sampled-out prompt was not stored: %+v %v", stored, err)
	}
}

func TestSampledOutCountsAreNotReviewOutcomes(t *testing.T) {
	counts := aggregate{}
	now := time.Now().UTC()
	counts.add(gateway.Count{ID: "exempt-count", Time: now, Protocol: "openai_chat", Outcome: "exempt"})
	counts.add(gateway.Count{ID: "sampled-count", Time: now, Protocol: "openai_chat", Outcome: "sampled"})
	if len(counts) != 2 {
		t.Fatalf("expected only two raw outcome counters, got %+v", counts)
	}
	for key, row := range counts {
		if key.Kind != "count" || row.Samples != 0 || row.SumMS != 0 {
			t.Fatalf("unreviewed request entered classifier aggregates: %+v %+v", key, row)
		}
	}
}
