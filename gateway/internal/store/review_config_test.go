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
	if _, err := s.pool.Exec(ctx, "UPDATE gateway_jev SET max_input_chars=NULL WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(ctx, dsn, spool)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SeedJevDefaults(ctx, 0); err != nil {
		t.Fatal(err)
	}
	c, err := s.Jev(ctx)
	if err != nil || c.MaxInputChars != 5000 {
		t.Fatalf("default not imported: %+v %v", c, err)
	}
	c.MaxInputChars = 20000
	if _, err := s.UpdateJev(ctx, c); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(ctx, dsn, spool)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SeedJevDefaults(ctx, 5000); err != nil {
		t.Fatal(err)
	}
	c, err = s.Jev(ctx)
	if err != nil || c.MaxInputChars != 20000 {
		t.Fatalf("restart overwrote administrator edit: %+v %v", c, err)
	}
}

func TestJevTokenLimitUpgradeResetsToDefaultCharacters(t *testing.T) {
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
	// Recreate the pre-upgrade schema with an administrator's old token limit.
	_, err = s.pool.Exec(ctx, `
		ALTER TABLE gateway_jev DROP COLUMN max_input_chars;
		ALTER TABLE gateway_jev ADD COLUMN max_input_tokens integer;
		UPDATE gateway_jev SET max_input_tokens=28800 WHERE id=1;
	`)
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dsn, spool)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := s.Jev(ctx)
	if err != nil || c.MaxInputChars != 5000 {
		t.Fatalf("old token limit was not reset to 5000 characters: %+v %v", c, err)
	}
	var legacyColumns int
	err = s.pool.QueryRow(ctx, `SELECT count(*) FROM pg_attribute WHERE attrelid='gateway_jev'::regclass AND attname='max_input_tokens' AND NOT attisdropped`).Scan(&legacyColumns)
	if err != nil || legacyColumns != 0 {
		t.Fatalf("legacy token column remains: %d %v", legacyColumns, err)
	}
}
