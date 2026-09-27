package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/cipherTing/sael/gateway/internal/gateway"
	"github.com/cipherTing/sael/gateway/internal/policy"
)

func TestCredentialEncryptionAuthenticatesIdentityAndMasksAfterDecrypt(t *testing.T) {
	c, err := newCredentialCipher(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	nonce, sealed, err := sealCredential(c, "identity", "sk-secret-value-5678")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("secret")) {
		t.Fatal("plaintext stored")
	}
	plaintext, err := openCredential(c, "identity", nonce, sealed)
	if err != nil || plaintext != "sk-secret-value-5678" || maskCredential(plaintext) != "sk-s********5678" {
		t.Fatal("credential did not roundtrip or was not masked", err)
	}
	if _, err := openCredential(c, "other", nonce, sealed); err == nil {
		t.Fatal("ciphertext can be moved between identities")
	}
	sealed[0] ^= 1
	if _, err := openCredential(c, "identity", nonce, sealed); err == nil {
		t.Fatal("tampering accepted")
	}
	if maskCredential("short") != "********" {
		t.Fatal("short key leaked")
	}
	if _, err := newCredentialCipher(""); err == nil {
		t.Fatal("missing master key accepted")
	}
}

func TestTrustedCredentialsSurviveExpiryForHistoricalEvents(t *testing.T) {
	p, r := redisFixture(t)
	ctx := context.Background()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	if err := p.InitCredentials(ctx, key); err != nil {
		t.Fatal(err)
	}
	s := &RedisStore{PG: p, redis: r}
	idle := 30 * 24 * time.Hour
	if err := s.CaptureCredential(ctx, "credential-id", "https://relay.example", "sk-complete-secret-5678", idle); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberKey(ctx, "credential-id", idle); err != nil {
		t.Fatal(err)
	}
	list, err := s.TrustedCredentials(ctx, idle, 0, 50)
	if err != nil || list.Total != 1 || len(list.Items) != 1 || list.Items[0].MaskedKey != "sk-c********5678" {
		t.Fatalf("list %+v %v", list, err)
	}
	event := gateway.Event{ID: "credential-event", RequestID: "request", Time: time.Now().UTC(), Kind: "hit", CredentialID: "credential-id", Decision: policy.Decision{Action: policy.Block}}
	if err := p.WriteEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	got, err := p.Event(ctx, event.ID)
	if err != nil || got.MaskedKey != "sk-c********5678" {
		t.Fatal("missing event key", err)
	}
	rows, err := p.Events(ctx, gateway.EventFilter{Since: event.Time.Add(-time.Minute), CredentialID: "credential-id"})
	if err != nil || len(rows) != 1 || rows[0].MaskedKey != got.MaskedKey {
		t.Fatal("list/detail disagree", err)
	}
	_ = r.ZAdd(ctx, trustedKeys, redis.Z{Score: float64(time.Now().Add(-31 * 24 * time.Hour).UnixMilli()), Member: "credential-id"}).Err()
	list, err = s.TrustedCredentials(ctx, idle, 0, 50)
	if err != nil || list.Total != 0 {
		t.Fatal("idle key still trusted", err)
	}
	_, _ = p.pool.Exec(ctx, "UPDATE gateway_credentials SET retained_at=now()-interval '40 days' WHERE id='credential-id'")
	if err := s.PruneCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	got, err = p.Event(ctx, event.ID)
	if err != nil || got.MaskedKey == "" {
		t.Fatal("historical association deleted", err)
	}
	if err := p.InitCredentials(ctx, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))); err == nil {
		t.Fatal("different master key accepted")
	}
}

func TestSuccessfulRevalidationRestoresCredentialAfterCleanup(t *testing.T) {
	p, r := redisFixture(t)
	ctx := context.Background()
	if err := p.InitCredentials(ctx, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))); err != nil {
		t.Fatal(err)
	}
	s := &RedisStore{PG: p, redis: r}
	idle := 30 * 24 * time.Hour
	if err := s.CaptureCredential(ctx, "relearn", "https://relay.example", "secret-client-key-9876", idle); err != nil {
		t.Fatal(err)
	}
	_, _ = p.pool.Exec(ctx, "DELETE FROM gateway_credentials WHERE id='relearn'")
	// The expired trust can leave a marker after an idle policy reduction.
	if err := s.RegisterCredential(ctx, "relearn", "https://relay.example", "secret-client-key-9876", idle); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberKey(ctx, "relearn", idle); err != nil {
		t.Fatal(err)
	}
	list, err := s.TrustedCredentials(ctx, idle, 0, 50)
	if err != nil || len(list.Items) != 1 || list.Items[0].MaskedKey != "secr********9876" {
		t.Fatal("stale marker suppressed ciphertext recovery", list, err)
	}
}
