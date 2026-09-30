package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cipherTing/sael/gateway/internal/gateway"
	"github.com/cipherTing/sael/gateway/internal/policy"
)

func TestPostgresEventListProjectsBoundedPreviewAndKeepsInternalFullText(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn, filepath.Join(t.TempDir(), "spool"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	id := "bounded-text-" + now.Format(time.RFC3339Nano)
	event := gateway.Event{ID: id, Time: now, Kind: "warning", RequestID: "bounded-text", Text: strings.Repeat("中", 600) + " password=never-store " + id, Decision: policy.Decision{Action: policy.Allow}}
	if err := s.WriteEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	wantText := strings.Repeat("中", 600) + " password=[已隐藏] " + id
	rows, err := s.Events(ctx, gateway.EventFilter{Since: now.Add(-time.Second), Search: id})
	if err != nil || len(rows) != 1 {
		t.Fatalf("list/search: %v rows=%d", err, len(rows))
	}
	if rows[0].Text != "" {
		t.Fatal("list query returned full text from PostgreSQL")
	}
	raw, _ := json.Marshal(rows[0])
	var projected map[string]any
	_ = json.Unmarshal(raw, &projected)
	if projected["text_available"] != true || projected["text_chars"] != float64(utf8.RuneCountInString(wantText)) || projected["text_preview"] != strings.Repeat("中", 500) {
		t.Fatalf("projected preview metadata: %s", raw)
	}
	full, err := s.Event(ctx, event.ID)
	if err != nil || full.Text != wantText {
		t.Fatalf("internal full-text read lost data: %v chars=%d", err, utf8.RuneCountInString(full.Text))
	}
}

func TestPostgresEventListReadsPreviewOnlyHistoryWithoutInventingFullText(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn, filepath.Join(t.TempDir(), "spool"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	id := "legacy-preview-only-" + now.Format(time.RFC3339Nano)
	event := gateway.Event{ID: id, Time: now, Kind: "warning", RequestID: id, TextPreview: strings.Repeat("旧", 700), Decision: policy.Decision{Action: policy.Allow}}
	if err := s.WriteEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Events(ctx, gateway.EventFilter{Since: now.Add(-time.Second), Search: id})
	if err != nil || len(rows) != 1 {
		t.Fatalf("legacy list: %v rows=%d", err, len(rows))
	}
	raw, _ := json.Marshal(rows[0])
	var projected map[string]any
	_ = json.Unmarshal(raw, &projected)
	if projected["text_available"] != false || projected["text_chars"] != float64(0) || projected["text_preview"] != strings.Repeat("旧", 500) {
		t.Fatalf("legacy preview metadata: %s", raw)
	}
	full, err := s.Event(ctx, event.ID)
	if err != nil || full.Text != "" || utf8.RuneCountInString(full.TextPreview) != 700 {
		t.Fatal("list projection changed historical stored content", err)
	}
}
