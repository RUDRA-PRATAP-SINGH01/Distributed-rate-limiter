package audit

import (
	"context"
	"testing"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/redistest"
)

func BenchmarkAuditAppend(b *testing.B) {
	cfg := DefaultConfig()
	cfg.Async = false
	store := NewStore(redistest.Start(b).Client(b), cfg)
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
	cfg := DefaultConfig()
	cfg.Async = false
	store := NewStore(redistest.Start(b).Client(b), cfg)
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		_, _ = store.record(ctx, RecordInput{UserID: "u1", Decision: DecisionAllowed, Reason: "x", Handler: "check"})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = store.Search(ctx, Query{UserID: "u1", Limit: 20})
	}
}
