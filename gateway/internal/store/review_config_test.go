package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPostgresFreezeSurvivesRestartAndExpires(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	spool := filepath.Join(t.TempDir(), "spool.jsonl")
	s, err := Open(ctx, dsn, spool)
	if err != nil {
		t.Fatal(err)
	}
	key, now := "freeze-persistence-test", time.Now().UTC()
	if _, err := s.pool.Exec(ctx, "DELETE FROM gateway_session_blocks WHERE session_hash=$1", key); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSessionBlock(ctx, key, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// A concurrent earlier request must not shorten the later deadline.
	if err := s.PutSessionBlock(ctx, key, now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(ctx, dsn, spool)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if active, err := s.SessionBlockActive(ctx, key, now.Add(45*time.Second)); err != nil || !active {
		t.Fatalf("lost freeze after restart: %v %v", active, err)
	}
	if active, err := s.SessionBlockActive(ctx, key, now.Add(time.Minute)); err != nil || active {
		t.Fatalf("freeze still active at expiry: %v %v", active, err)
	}
}

func TestJevInputDefaultIsSeededOnceAndConsoleEditsSurviveRestart(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	spool := filepath.Join(t.TempDir(), "spool.jsonl")
	s, err := Open(ctx, dsn, spool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE gateway_jev SET max_input_tokens=NULL WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SeedJevDefaults(ctx, 25000); err != nil {
		t.Fatal(err)
	}
	c, err := s.Jev(ctx)
	if err != nil || c.MaxInputTokens != 25000 {
		t.Fatalf("default not imported: %+v %v", c, err)
	}
	c.MaxInputTokens = 20000
	if _, err := s.UpdateJev(ctx, c); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(ctx, dsn, spool)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SeedJevDefaults(ctx, 28800); err != nil {
		t.Fatal(err)
	}
	c, err = s.Jev(ctx)
	if err != nil || c.MaxInputTokens != 20000 {
		t.Fatalf("restart overwrote administrator edit: %+v %v", c, err)
	}
}
