package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cipherTing/sael/gateway/internal/gateway"
	"github.com/cipherTing/sael/gateway/internal/policy"
)

func performanceDatabase(b *testing.B) *PG {
	b.Helper()
	dsn := os.Getenv("SAEL_PERF_DATABASE_URL")
	if dsn == "" {
		b.Skip("SAEL_PERF_DATABASE_URL not set; must point to an isolated benchmark database")
	}
	s, err := Open(context.Background(), dsn, filepath.Join(b.TempDir(), "spool.jsonl"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(s.Close)
	return s
}
func BenchmarkConfigurationReads(b *testing.B) {
	s := performanceDatabase(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := s.Policy(ctx); err != nil {
			b.Fatal(err)
		}
		if _, err := s.Jev(ctx); err != nil {
			b.Fatal(err)
		}
		if _, err := s.Upstream(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkCountPersistence(b *testing.B) {
	s := performanceDatabase(b)
	ctx := context.Background()
	c := gateway.Count{Time: time.Now().UTC(), Protocol: "openai_responses", Model: "performance", Outcome: "clean", ClassifierSample: true, ClassifierMS: 150, JevMS: 145}
	for _, q := range policy.Questions {
		c.Scores = append(c.Scores, policy.Answer{Question: q.Key, Type: q.Type, Value: .1})
	}
	for _, parallel := range []bool{false, true} {
		name := "serial"
		if parallel {
			name = "parallel"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			if parallel {
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						if err := s.Increment(ctx, c); err != nil {
							b.Error(err)
							return
						}
					}
				})
			} else {
				for b.Loop() {
					if err := s.Increment(ctx, c); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

func BenchmarkCachedConfigurationReads(b *testing.B) {
	p := performanceDatabase(b)
	endpoint := os.Getenv("TEST_REDIS_URL")
	if endpoint == "" {
		b.Skip("TEST_REDIS_URL not set")
	}
	ctx := context.Background()
	s, err := OpenRedis(ctx, p, endpoint)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(s.Close)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := s.Policy(ctx); err != nil {
			b.Fatal(err)
		}
		if _, err := s.Jev(ctx); err != nil {
			b.Fatal(err)
		}
		if _, err := s.Upstream(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
