package circuitbreaker

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func BenchmarkCircuitAllow(b *testing.B) {
	mr, _ := miniredis.Run()
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	br := NewBreaker(NewRedisStore(rdb, DefaultConfig()))
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = br.Allow(ctx, "bench-target")
	}
}

func BenchmarkCircuitRecord(b *testing.B) {
	mr, _ := miniredis.Run()
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	br := NewBreaker(NewRedisStore(rdb, DefaultConfig()))
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = br.Allow(ctx, "bench-target")
		_ = br.Record(ctx, "bench-target", RecordInput{
			Kind:    OutcomeSuccess,
			Latency: 12 * time.Millisecond,
		})
	}
}

func BenchmarkCircuitAllowRecordParallel(b *testing.B) {
	mr, _ := miniredis.Run()
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	br := NewBreaker(NewRedisStore(rdb, DefaultConfig()))
	ctx := context.Background()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = br.Allow(ctx, "bench-target")
			_ = br.Record(ctx, "bench-target", RecordInput{
				Kind:    OutcomeSuccess,
				Latency: 8 * time.Millisecond,
			})
		}
	})
}
