package store

import (
	"context"
	"time"
)

// PutSessionBlock stores only a one-way session hash and its expiry.
func (s *PG) PutSessionBlock(ctx context.Context, sessionHash string, until time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO gateway_session_blocks(session_hash,expires_at,updated_at)
		VALUES ($1,$2,now())
		ON CONFLICT (session_hash) DO UPDATE SET expires_at=GREATEST(gateway_session_blocks.expires_at, EXCLUDED.expires_at), updated_at=now()`, sessionHash, until)
	return err
}

// SessionBlockActive tests expiry atomically without racing concurrent renewal.
func (s *PG) SessionBlockActive(ctx context.Context, sessionHash string, now time.Time) (bool, error) {
	var active bool
	err := s.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM gateway_session_blocks WHERE session_hash=$1 AND expires_at>$2)", sessionHash, now).Scan(&active)
	return active, err
}
