package store

import (
	"context"
	"strings"
	"time"
)

// PutSessionBlock stores only a one-way session hash and its expiry.
func (s *PG) PutSessionBlock(ctx context.Context, sessionHash string, until time.Time) error {
	if strings.HasPrefix(sessionHash, "scope:") {
		_, err := s.pool.Exec(ctx, `INSERT INTO gateway_session_blocks(session_hash,expires_at,updated_at) VALUES($1,$2,now()) ON CONFLICT(session_hash) DO UPDATE SET expires_at=greatest(gateway_session_blocks.expires_at,EXCLUDED.expires_at),updated_at=now()`, sessionHash, until)
		return err
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO gateway_session_blocks(session_hash,expires_at,updated_at)
		VALUES ($1,$2,now())
		ON CONFLICT (session_hash) DO UPDATE SET expires_at=CASE
			WHEN gateway_session_blocks.expires_at <= now() THEN EXCLUDED.expires_at
			ELSE gateway_session_blocks.expires_at
		END, updated_at=now()`, sessionHash, until)
	return err
}

// SessionBlockActive tests expiry atomically without racing concurrent renewal.
func (s *PG) SessionBlockActive(ctx context.Context, sessionHash string, now time.Time) (bool, error) {
	var active bool
	err := s.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM gateway_session_blocks WHERE session_hash=$1 AND expires_at>$2)", sessionHash, now).Scan(&active)
	return active, err
}

// FindSessionBlock preserves candidate order while reading active blocks together.
func (s *PG) FindSessionBlock(ctx context.Context, keys []string, now time.Time) (string, error) {
	if len(keys) == 0 {
		return "", nil
	}
	rows, err := s.pool.Query(ctx, "SELECT session_hash FROM gateway_session_blocks WHERE session_hash=ANY($1) AND expires_at>$2", keys, now)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	active := make(map[string]bool, len(keys))
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return "", err
		}
		active[key] = true
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	for _, key := range keys {
		if active[key] {
			return key, nil
		}
	}
	return "", nil
}
