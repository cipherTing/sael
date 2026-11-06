package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/cipherTing/sael/gateway/internal/gateway"
)

func TestCredentialNotesPersistUntilCredentialCleanup(t *testing.T) {
	p, r := redisFixture(t)
	ctx := context.Background()
	if err := p.InitCredentials(ctx, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))); err != nil {
		t.Fatal(err)
	}
	s := &RedisStore{PG: p, redis: r}
	idle := 30 * 24 * time.Hour
	const id, secret = "notes-lifecycle", "sk-notes-lifecycle-secret"
	t.Cleanup(func() { _, _ = p.pool.Exec(ctx, "DELETE FROM gateway_credentials WHERE id=$1", id) })
	if err := s.RegisterCredential(ctx, id, "https://relay.example", secret, idle); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberKey(ctx, id, idle); err != nil {
		t.Fatal(err)
	}
	list, err := s.TrustedCredentials(ctx, idle, gateway.TrustedCredentialFilter{Limit: 50})
	if err != nil || len(list.Items) != 1 || list.Items[0].Note != "" {
		t.Fatalf("default note: %+v %v", list, err)
	}
	if err := p.SetCredentialNote(ctx, id, "生产环境"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterCredential(ctx, id, "https://relay.example", secret, idle); err != nil {
		t.Fatal(err)
	}
	list, err = s.TrustedCredentials(ctx, idle, gateway.TrustedCredentialFilter{Limit: 50})
	if err != nil || len(list.Items) != 1 || list.Items[0].Note != "生产环境" {
		t.Fatalf("revalidation overwrote note: %+v %v", list, err)
	}
	if err := p.SetCredentialNote(ctx, id, ""); err != nil {
		t.Fatal(err)
	}
	list, err = s.TrustedCredentials(ctx, idle, gateway.TrustedCredentialFilter{Limit: 50})
	if err != nil || len(list.Items) != 1 || list.Items[0].Note != "" {
		t.Fatalf("cleared note: %+v %v", list, err)
	}
	if err := p.SetCredentialNote(ctx, "missing-notes-credential", "unknown"); !errors.Is(err, gateway.ErrNotFound) {
		t.Fatalf("unknown credential err=%v", err)
	}
	if err := p.SetCredentialNote(ctx, id, "delete with credential"); err != nil {
		t.Fatal(err)
	}
	if err := s.ForgetKey(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := p.pool.Exec(ctx, "UPDATE gateway_credentials SET retained_at=now()-interval '2 days' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterCredential(ctx, id, "https://relay.example", secret, idle); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberKey(ctx, id, idle); err != nil {
		t.Fatal(err)
	}
	list, err = s.TrustedCredentials(ctx, idle, gateway.TrustedCredentialFilter{Limit: 50})
	if err != nil || len(list.Items) != 1 || list.Items[0].Note != "" {
		t.Fatalf("deleted credential retained note: %+v %v", list, err)
	}
}

func TestTrustedCredentialFiltersScanAllActiveKeysBeforePaging(t *testing.T) {
	p, r := redisFixture(t)
	ctx := context.Background()
	if err := p.InitCredentials(ctx, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))); err != nil {
		t.Fatal(err)
	}
	s := &RedisStore{PG: p, redis: r}
	idle := 30 * 24 * time.Hour
	now := time.Now().UTC()
	const total = 270
	ids := make([]string, 0, total+2)
	t.Cleanup(func() { _, _ = p.pool.Exec(ctx, "DELETE FROM gateway_credentials WHERE id=ANY($1)", ids) })
	for i := 0; i < total+2; i++ {
		id := fmt.Sprintf("notes-filter-%03d", i)
		ids = append(ids, id)
		secret := fmt.Sprintf("sk-note-filter-%03d-private-content-never-returned", i)
		if err := s.RegisterCredential(ctx, id, "https://relay.example", secret, idle); err != nil {
			t.Fatal(err)
		}
		if i == 3 || i == 130 || i == 259 || i >= total {
			if err := p.SetCredentialNote(ctx, id, "共享 Ops_% Team"); err != nil {
				t.Fatal(err)
			}
		}
		if i < total {
			if err := r.ZAdd(ctx, trustedKeys, redis.Z{Score: float64(now.Add(-time.Duration(i) * time.Second).UnixMilli()), Member: id}).Err(); err != nil {
				t.Fatal(err)
			}
		}
	}
	// One key is expired; another has historical database metadata without active trust.
	if err := r.ZAdd(ctx, trustedKeys, redis.Z{Score: float64(now.Add(-31 * 24 * time.Hour).UnixMilli()), Member: ids[total]}).Err(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		filter gateway.TrustedCredentialFilter
		total  int64
		want   []string
	}{
		{name: "unfiltered page", filter: gateway.TrustedCredentialFilter{Offset: 50, Limit: 2}, total: total, want: []string{"notes-filter-050", "notes-filter-051"}},
		{name: "case insensitive note across batches", filter: gateway.TrustedCredentialFilter{Note: "oPs_%", Limit: 50}, total: 3, want: []string{"notes-filter-003", "notes-filter-130", "notes-filter-259"}},
		{name: "filter before offset", filter: gateway.TrustedCredentialFilter{Note: "Ops", Offset: 1, Limit: 1}, total: 3, want: []string{"notes-filter-130"}},
		{name: "prefix across masked region", filter: gateway.TrustedCredentialFilter{KeyPrefix: "sk-note-filter-259-private", Limit: 50}, total: 1, want: []string{"notes-filter-259"}},
		{name: "prefix case sensitive", filter: gateway.TrustedCredentialFilter{KeyPrefix: "sk-NOTE-filter-", Limit: 50}, total: 0},
		{name: "literal percent", filter: gateway.TrustedCredentialFilter{Note: "%", Limit: 50}, total: 3, want: []string{"notes-filter-003", "notes-filter-130", "notes-filter-259"}},
		{name: "literal underscore", filter: gateway.TrustedCredentialFilter{Note: "_", Limit: 50}, total: 3, want: []string{"notes-filter-003", "notes-filter-130", "notes-filter-259"}},
		{name: "combined", filter: gateway.TrustedCredentialFilter{KeyPrefix: "sk-note-filter-259", Note: "team", Limit: 50}, total: 1, want: []string{"notes-filter-259"}},
		{name: "conditions are and", filter: gateway.TrustedCredentialFilter{KeyPrefix: "sk-note-filter-258", Note: "team", Limit: 50}, total: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.TrustedCredentials(ctx, idle, tc.filter)
			if err != nil || got.Total != tc.total || len(got.Items) != len(tc.want) {
				t.Fatalf("filtered page=%+v err=%v", got, err)
			}
			for i, want := range tc.want {
				if got.Items[i].ID != want || !strings.Contains(got.Items[i].MaskedKey, "*") {
					t.Fatalf("wrong order or unmasked key: %+v want=%v", got, tc.want)
				}
			}
		})
	}
}
