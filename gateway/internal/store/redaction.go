package store

import (
	"context"

	"github.com/cipherTing/sael/gateway/internal/privacy"
)

// redactHistoricalEvents upgrades old full-text records once, without removing their audit data.
func (s *PG) redactHistoricalEvents(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('sael:stored-prompt-redaction')); CREATE TABLE IF NOT EXISTS gateway_data_migrations (name text PRIMARY KEY)`); err != nil {
		return err
	}
	var done bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gateway_data_migrations WHERE name='prompt-redaction-v1')`).Scan(&done); err != nil {
		return err
	}
	if done {
		return tx.Commit(ctx)
	}
	type record struct{ id, text, preview string }
	last := ""
	for {
		rows, err := tx.Query(ctx, `SELECT id,coalesce(body->>'text',''),coalesce(body->>'text_preview','') FROM audit_events WHERE id > $1 ORDER BY id LIMIT 200`, last)
		if err != nil {
			return err
		}
		batch := []record{}
		for rows.Next() {
			var r record
			if err := rows.Scan(&r.id, &r.text, &r.preview); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		for _, r := range batch {
			text, preview := privacy.RedactText(r.text), privacy.RedactText(r.preview)
			if text != r.text || preview != r.preview {
				if _, err = tx.Exec(ctx, `UPDATE audit_events SET body=jsonb_set(jsonb_set(body,'{text}',to_jsonb($2::text)),'{text_preview}',to_jsonb($3::text)) WHERE id=$1`, r.id, text, preview); err != nil {
					return err
				}
			}
		}
		last = batch[len(batch)-1].id
	}
	if _, err = tx.Exec(ctx, `INSERT INTO gateway_data_migrations(name) VALUES ('prompt-redaction-v1')`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
