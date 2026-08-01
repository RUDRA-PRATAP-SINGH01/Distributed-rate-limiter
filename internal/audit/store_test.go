package audit

import (
	"context"
	"testing"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/redistest"
)

func setupAudit(t *testing.T) (*Store, *redistest.Server) {
	t.Helper()
	srv := redistest.Start(t)
	cfg := DefaultConfig()
	cfg.Async = false
	cfg.Retention = time.Hour
	cfg.MaxEvents = 1000
	return NewStore(srv.Client(t), cfg), srv
}

func TestRecordAndGet(t *testing.T) {
	store, _ := setupAudit(t)
	ctx := context.Background()

	ev, err := store.record(ctx, RecordInput{
		RequestID: "req-1",
		TenantID:  "tenant-a",
		UserID:    "user-1",
		Decision:  DecisionAllowed,
		Reason:    "check: quota available",
		Handler:   "check",
		Remaining: 9,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := store.Get(ctx, ev.ID)
	if err != nil || got == nil || got.UserID != "user-1" {
		t.Fatalf("get: %#v %v", got, err)
	}

	byReq, err := store.GetByRequestID(ctx, "req-1")
	if err != nil || byReq == nil || byReq.ID != ev.ID {
		t.Fatalf("by request: %#v %v", byReq, err)
	}
}

func TestSearchFilters(t *testing.T) {
	store, _ := setupAudit(t)
	ctx := context.Background()

	_, _ = store.record(ctx, RecordInput{RequestID: "r1", TenantID: "t1", UserID: "u1", Decision: DecisionAllowed, Reason: "ok", Handler: "check", Remaining: 5})
	_, _ = store.record(ctx, RecordInput{RequestID: "r2", TenantID: "t1", UserID: "u2", Decision: DecisionDenied, Reason: "limit", Handler: "check", Remaining: 0})

	results, err := store.Search(ctx, Query{TenantID: "t1", Decision: DecisionDenied})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].UserID != "u2" {
		t.Fatalf("expected one denial for u2, got %+v", results)
	}
}

func TestReplay(t *testing.T) {
	store, _ := setupAudit(t)
	ctx := context.Background()
	ev, _ := store.record(ctx, RecordInput{RequestID: "r3", UserID: "u3", Decision: DecisionDenied, Reason: "limit", Handler: "check"})
	payload, err := store.Replay(ctx, ev.ID)
	if err != nil || payload == nil || payload.ReplayHint == "" {
		t.Fatalf("replay: %#v %v", payload, err)
	}
}

func TestIndexCleanupOnTrim(t *testing.T) {
	store, _ := setupAudit(t)
	ctx := context.Background()
	store.cfg.MaxEvents = 1

	ev1, _ := store.record(ctx, RecordInput{TenantID: "t1", UserID: "u1", Decision: DecisionAllowed, Reason: "a", Handler: "check"})
	_, _ = store.record(ctx, RecordInput{TenantID: "t1", UserID: "u1", Decision: DecisionAllowed, Reason: "b", Handler: "check"})

	tenantN, _ := store.rdb.ZCard(ctx, tenantIndexKey("t1")).Result()
	userN, _ := store.rdb.ZCard(ctx, userIndexKey("u1")).Result()
	if tenantN != 1 || userN != 1 {
		t.Fatalf("indexes should have one member after trim: tenant=%d user=%d", tenantN, userN)
	}
	if got, _ := store.Get(ctx, ev1.ID); got != nil {
		t.Fatal("oldest event should be purged from tenant/user indexes")
	}
}

func TestRetentionTrim(t *testing.T) {
	store, srv := setupAudit(t)
	ctx := context.Background()
	store.cfg.Retention = time.Millisecond * 100
	store.cfg.MaxEvents = 2

	_, _ = store.record(ctx, RecordInput{UserID: "u", Decision: DecisionAllowed, Reason: "a", Handler: "check"})
	_, _ = store.record(ctx, RecordInput{UserID: "u", Decision: DecisionAllowed, Reason: "b", Handler: "check"})
	_, _ = store.record(ctx, RecordInput{UserID: "u", Decision: DecisionAllowed, Reason: "c", Handler: "check"})

	stats, _ := store.Stats(ctx)
	if stats["events_indexed"] > 2 {
		t.Fatalf("expected max_events trim, got %d", stats["events_indexed"])
	}
	srv.FastForward(time.Second)
}
