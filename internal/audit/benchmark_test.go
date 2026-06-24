package audit

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func BenchmarkAuditAppend(b *testing.B) {
	mr, _ := miniredis.Run()
	defer mr.Close()
	cfg := DefaultConfig()
	cfg.Async = false
	store := NewStore(redis.NewClient(&redis.Options{Addr: mr.Addr()}), cfg)
	ctx := context.Background()
	in := RecordInput{
		RequestID: "bench-req",
		TenantID:  "t1",
		UserID:    "u1",
		Decision:  DecisionAllowed,
		Reason:    "bench",
		Handler:   "check",
		Remaining: 5,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = store.record(ctx, in)
	}
}

func BenchmarkAuditSearch(b *testing.B) {
	mr, _ := miniredis.Run()
	defer mr.Close()
	cfg := DefaultConfig()
	cfg.Async = false
	store := NewStore(redis.NewClient(&redis.Options{Addr: mr.Addr()}), cfg)
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		_, _ = store.record(ctx, RecordInput{UserID: "u1", Decision: DecisionAllowed, Reason: "x", Handler: "check"})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = store.Search(ctx, Query{UserID: "u1", Limit: 20})
	}
}
