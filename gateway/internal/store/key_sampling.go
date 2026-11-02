package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/cipherTing/sael/gateway/internal/gateway"
)

// KeySampleRate returns a credential's configured rate, defaulting to 100 percent.
func (s *PG) KeySampleRate(ctx context.Context, fingerprint string) (int, error) {
	if fingerprint == "" {
		return 100, nil
	}
	var rate int
	err := s.pool.QueryRow(ctx, "SELECT sample_rate FROM gateway_key_sampling WHERE fingerprint=$1", fingerprint).Scan(&rate)
	if errors.Is(err, pgx.ErrNoRows) {
		return 100, nil
	}
	return rate, err
}

// SetKeySampleRate updates a credential's review sampling percentage.
func (s *PG) SetKeySampleRate(ctx context.Context, fingerprint string, rate int) error {
	if rate < 0 || rate > 100 {
		return errors.New("sample rate must be between 0 and 100")
	}
	result, err := s.pool.Exec(ctx, "UPDATE gateway_key_sampling SET sample_rate=$2 WHERE fingerprint=$1", fingerprint, rate)
	if err != nil {
		return err
	}
	if result.RowsAffected() > 0 {
		return nil
	}
	var upstream string
	var nonce, ciphertext []byte
	err = s.pool.QueryRow(ctx, "SELECT upstream,nonce,ciphertext FROM gateway_credentials WHERE id=$1", fingerprint).Scan(&upstream, &nonce, &ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return gateway.ErrNotFound
	}
	if err != nil {
		return err
	}
	secret, err := openCredential(s.credentialCipher, fingerprint, nonce, ciphertext)
	if err != nil {
		return err
	}
	if gateway.CredentialFingerprint(upstream, secret) != fingerprint {
		return errors.New("stored credential identity mismatch")
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO gateway_key_sampling(fingerprint,upstream,masked_key,sample_rate)
VALUES($1,$2,$3,$4) ON CONFLICT(fingerprint) DO UPDATE SET sample_rate=EXCLUDED.sample_rate`, fingerprint, upstream, maskCredential(secret), rate)
	return err
}
