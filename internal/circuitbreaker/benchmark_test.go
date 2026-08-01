package circuitbreaker

import (
	"context"
	"testing"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/redistest"
)

func BenchmarkCircuitAllow(b *testing.B) {
	br := NewBreaker(NewRedisStore(redistest.Start(b).Client(b), DefaultConfig()))
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = br.Allow(ctx, "bench-target")
	}
}

func BenchmarkCircuitRecord(b *testing.B) {
	br := NewBreaker(NewRedisStore(redistest.Start(b).Client(b), DefaultConfig()))
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
	br := NewBreaker(NewRedisStore(redistest.Start(b).Client(b), DefaultConfig()))
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
