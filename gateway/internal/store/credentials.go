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
	return string(r[:left]) + strings.Repeat("*", hidden) + string(r[left+hidden:])
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

// TrustedCredentials lists active trust with decrypted, masked credential metadata.
func (s *RedisStore) TrustedCredentials(ctx context.Context, idle time.Duration, offset, limit int) (gateway.TrustedCredentials, error) {
	out := gateway.TrustedCredentials{Items: []gateway.TrustedCredential{}}
	if err := s.PruneTrustedKeys(ctx, idle); err != nil {
		return out, err
	}
	total, err := s.redis.ZCard(ctx, trustedKeys).Result()
	if err != nil {
		return out, err
	}
	out.Total = total
	entries, err := s.redis.ZRevRangeWithScores(ctx, trustedKeys, int64(offset), int64(offset+limit-1)).Result()
	if err != nil {
		return out, err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.Member.(string))
	}
	rows, err := s.pool.Query(ctx, "SELECT id,upstream,nonce,ciphertext,first_seen_at FROM gateway_credentials WHERE id=ANY($1)", ids)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	values := map[string]gateway.TrustedCredential{}
	for rows.Next() {
		var v gateway.TrustedCredential
		var nonce, sealed []byte
		if err := rows.Scan(&v.ID, &v.Upstream, &nonce, &sealed, &v.FirstSeenAt); err != nil {
			return out, err
		}
		raw, err := openCredential(s.credentialCipher, v.ID, nonce, sealed)
		if err != nil {
			return out, err
		}
		v.MaskedKey = maskCredential(raw)
		values[v.ID] = v
	}
	for _, e := range entries {
		id := e.Member.(string)
		v := values[id]
		v.ID = id
		v.LastSeenAt = time.UnixMilli(int64(e.Score)).UTC()
		out.Items = append(out.Items, v)
	}
	return out, rows.Err()
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
