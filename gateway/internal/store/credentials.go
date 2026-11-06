package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/cipherTing/sael/gateway/internal/gateway"
)

func newCredentialCipher(key string) (cipher.AEAD, error) {
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(raw) != 32 {
		return nil, errors.New("CREDENTIAL_ENCRYPTION_KEY 必须是 Base64 编码的 32 字节密钥")
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func sealCredential(c cipher.AEAD, id, raw string) (nonce, sealed []byte, err error) {
	nonce = make([]byte, c.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, c.Seal(nil, nonce, []byte(raw), []byte(id)), nil
}
func openCredential(c cipher.AEAD, id string, nonce, sealed []byte) (string, error) {
	if c == nil || len(nonce) != c.NonceSize() {
		return "", errors.New("凭据加密配置或密文无效")
	}
	raw, err := c.Open(nil, nonce, sealed, []byte(id))
	if err != nil {
		return "", errors.New("凭据解密失败，请检查主密钥")
	}
	return string(raw), nil
}
func maskCredential(raw string) string {
	r := []rune(raw)
	hidden := (len(r) + 1) / 2
	left := (len(r) - hidden) / 2
	return string(r[:left]) + strings.Repeat("*", min(hidden, 6)) + string(r[left+hidden:])
}

// InitCredentials verifies the persistent key before accepting any requests.
func (s *PG) InitCredentials(ctx context.Context, key string) error {
	c, err := newCredentialCipher(key)
	if err != nil {
		return err
	}
	nonce, sealed, err := sealCredential(c, "master-key", "sael-credentials-v1")
	if err != nil {
		return err
	}
	if _, err = s.pool.Exec(ctx, "INSERT INTO gateway_credential_key(id,nonce,ciphertext) VALUES(1,$1,$2) ON CONFLICT DO NOTHING", nonce, sealed); err != nil {
		return err
	}
	if err := s.pool.QueryRow(ctx, "SELECT nonce,ciphertext FROM gateway_credential_key WHERE id=1").Scan(&nonce, &sealed); err != nil {
		return err
	}
	if _, err = openCredential(c, "master-key", nonce, sealed); err != nil {
		return err
	}
	s.credentialCipher = c
	return nil
}
func (s *PG) saveCredential(ctx context.Context, id, target, raw string) error {
	if s.credentialCipher == nil {
		return errors.New("凭据加密未初始化")
	}
	nonce, sealed, err := sealCredential(s.credentialCipher, id, raw)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, "INSERT INTO gateway_credentials(id,upstream,nonce,ciphertext) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO UPDATE SET retained_at=now()", id, target, nonce, sealed)
	return err
}

// CaptureCredential performs one DB insert per newly verified credential. Redis
// markers have the same idle lifetime as trust, and allow lazy legacy backfill.
func (s *RedisStore) CaptureCredential(ctx context.Context, id, target, raw string, idle time.Duration) error {
	if id == "" || raw == "" {
		return nil
	}
	marker := "sael:credential:" + id
	if _, err := s.redis.GetEx(ctx, marker, idle).Result(); err == nil {
		return nil
	} else if !errors.Is(err, redis.Nil) {
		return err
	}
	_, err, _ := s.credentialWrites.Do(id, func() (any, error) {
		if err := s.saveCredential(ctx, id, target, raw); err != nil {
			return nil, err
		}
		return nil, s.redis.Set(ctx, marker, "1", idle).Err()
	})
	return err
}

// RegisterCredential persists every new proof of validity, including revalidation
// after expiry. An old Redis marker cannot substitute for this durable write.
func (s *RedisStore) RegisterCredential(ctx context.Context, id, target, raw string, idle time.Duration) error {
	if err := s.saveCredential(ctx, id, target, raw); err != nil {
		return err
	}
	return s.redis.Set(ctx, "sael:credential:"+id, "1", idle).Err()
}

func (s *PG) hydrateCredentials(ctx context.Context, events []gateway.Event) error {
	ids := []string{}
	for _, e := range events {
		if e.CredentialID != "" {
			ids = append(ids, e.CredentialID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.pool.Query(ctx, "SELECT id,nonce,ciphertext FROM gateway_credentials WHERE id=ANY($1)", ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	masks := map[string]string{}
	for rows.Next() {
		var id string
		var nonce, sealed []byte
		if err := rows.Scan(&id, &nonce, &sealed); err != nil {
			return err
		}
		raw, err := openCredential(s.credentialCipher, id, nonce, sealed)
		if err != nil {
			return err
		}
		masks[id] = maskCredential(raw)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range events {
		events[i].MaskedKey = masks[events[i].CredentialID]
	}
	return nil
}

// SetCredentialNote changes only the administrative note of an existing credential.
func (s *PG) SetCredentialNote(ctx context.Context, id, note string) error {
	result, err := s.pool.Exec(ctx, "UPDATE gateway_credentials SET note=$2 WHERE id=$1", id, postgresText(note))
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return gateway.ErrNotFound
	}
	return nil
}

// TrustedCredentials lists active trust with decrypted, masked credential metadata.
func (s *RedisStore) TrustedCredentials(ctx context.Context, idle time.Duration, filter gateway.TrustedCredentialFilter) (gateway.TrustedCredentials, error) {
	out := gateway.TrustedCredentials{Items: []gateway.TrustedCredential{}}
	filter.KeyPrefix = strings.TrimSpace(filter.KeyPrefix)
	filter.Note = strings.ToLower(strings.TrimSpace(filter.Note))
	filter.Offset = max(0, filter.Offset)
	if filter.Limit < 1 || filter.Limit > 100 {
		filter.Limit = 50
	}
	if err := s.PruneTrustedKeys(ctx, idle); err != nil {
		return out, err
	}
	filtered := filter.KeyPrefix != "" || filter.Note != ""
	start, stop := int64(filter.Offset), int64(filter.Offset+filter.Limit-1)
	if filtered {
		start, stop = 0, -1
	} else {
		total, err := s.redis.ZCard(ctx, trustedKeys).Result()
		if err != nil {
			return out, err
		}
		out.Total = total
	}
	entries, err := s.redis.ZRevRangeWithScores(ctx, trustedKeys, start, stop).Result()
	if err != nil {
		return out, err
	}
	const batchSize = 128
	for begin := 0; begin < len(entries); begin += batchSize {
		batch := entries[begin:min(begin+batchSize, len(entries))]
		values, err := s.trustedCredentialValues(ctx, batch, filter)
		if err != nil {
			return out, err
		}
		for _, entry := range batch {
			id := entry.Member.(string)
			value, ok := values[id]
			if !ok {
				if filtered {
					continue
				}
				value = gateway.TrustedCredential{ID: id, SampleRate: 100}
			}
			value.LastSeenAt = time.UnixMilli(int64(entry.Score)).UTC()
			if filtered {
				out.Total++
				if out.Total <= int64(filter.Offset) || len(out.Items) == filter.Limit {
					continue
				}
			}
			out.Items = append(out.Items, value)
		}
	}
	return out, nil
}

func (s *RedisStore) trustedCredentialValues(ctx context.Context, entries []redis.Z, filter gateway.TrustedCredentialFilter) (map[string]gateway.TrustedCredential, error) {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.Member.(string))
	}
	rows, err := s.pool.Query(ctx, `SELECT c.id,c.upstream,c.nonce,c.ciphertext,c.first_seen_at,coalesce(k.sample_rate,100),c.note
FROM gateway_credentials c LEFT JOIN gateway_key_sampling k ON k.fingerprint=c.id
WHERE c.id=ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make(map[string]gateway.TrustedCredential, len(entries))
	for rows.Next() {
		var value gateway.TrustedCredential
		var nonce, sealed []byte
		if err := rows.Scan(&value.ID, &value.Upstream, &nonce, &sealed, &value.FirstSeenAt, &value.SampleRate, &value.Note); err != nil {
			return nil, err
		}
		if !strings.Contains(strings.ToLower(value.Note), filter.Note) {
			continue
		}
		raw, err := openCredential(s.credentialCipher, value.ID, nonce, sealed)
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(raw, filter.KeyPrefix) {
			continue
		}
		value.MaskedKey = maskCredential(raw)
		values[value.ID] = value
	}
	return values, rows.Err()
}

// PruneCredentials removes untrusted credentials without retained audit references.
func (s *RedisStore) PruneCredentials(ctx context.Context) error {
	// Retain credentials while accepted telemetry is still awaiting persistence.
	if len(s.queue) > 0 {
		return nil
	}
	last, err := s.cursor(ctx)
	if err != nil {
		return err
	}
	pending, err := s.redis.XRangeN(ctx, ingestStream, "("+last, "+", 1).Result()
	if err != nil || len(pending) > 0 {
		return err
	}
	s.spoolMu.Lock()
	spooling := s.spooling
	s.spoolMu.Unlock()
	if spooling {
		return nil
	}
	ids, err := s.redis.ZRange(ctx, trustedKeys, 0, -1).Result()
	if err != nil {
		return err
	}
	rows, err := s.pool.Query(ctx, `DELETE FROM gateway_credentials c WHERE retained_at < now()-interval '1 day' AND NOT(id=ANY($1::text[])) AND NOT EXISTS(SELECT 1 FROM audit_events e WHERE e.body->>'credential_id'=c.id) RETURNING id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	markers := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		markers = append(markers, "sael:credential:"+id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if len(markers) > 0 {
		return s.redis.Del(ctx, markers...).Err()
	}
	return nil
}
