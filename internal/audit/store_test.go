package audit

import (
	"context"
	"fmt"
	"testing"
	"time"

	redisclient "github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/redis"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/redistest"
	"github.com/redis/go-redis/v9"
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

func TestAuditIndex_TTLSetOnAllIndexes(t *testing.T) {
	store, _ := setupAudit(t)
	ctx := context.Background()

	_, err := store.record(ctx, RecordInput{
		TenantID: "tenant-ttl",
		UserID:   "user-ttl",
		Decision: DecisionAllowed,
		Reason:   "ok",
		Handler:  "check",
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	// Verify all index ZSETs received positive TTL.
	for _, key := range []string{tsIndexKey(), tenantIndexKey("tenant-ttl"), userIndexKey("user-ttl")} {
		ttl, err := store.rdb.TTL(ctx, key).Result()
		if err != nil {
			t.Fatalf("TTL(%s): %v", key, err)
		}
		if ttl <= 0 {
			t.Errorf("expected positive TTL on %s, got %v", key, ttl)
		}
	}
}

func TestAuditIndex_ZeroCardinalityDeletedOnTrim(t *testing.T) {
	store, _ := setupAudit(t)
	ctx := context.Background()
	store.cfg.MaxEvents = 1

	// Record event for ephemeral user u1 (tenant t1).
	_, err := store.record(ctx, RecordInput{
		TenantID: "t-ephemeral-1",
		UserID:   "u-ephemeral-1",
		Decision: DecisionAllowed,
		Reason:   "test",
		Handler:  "check",
	})
	if err != nil {
		t.Fatalf("record 1: %v", err)
	}

	u1Key := userIndexKey("u-ephemeral-1")
	exists1, err := store.rdb.Exists(ctx, u1Key).Result()
	if err != nil || exists1 != 1 {
		t.Fatalf("expected user index %s to exist initially: %v", u1Key, err)
	}

	// Record event for ephemeral user u2 -> global max_events=1 causes u1's event to be purged.
	_, err = store.record(ctx, RecordInput{
		TenantID: "t-ephemeral-2",
		UserID:   "u-ephemeral-2",
		Decision: DecisionAllowed,
		Reason:   "test",
		Handler:  "check",
	})
	if err != nil {
		t.Fatalf("record 2: %v", err)
	}

	// u1's user index should have been DEL'd because ZCARD became 0.
	existsAfter, err := store.rdb.Exists(ctx, u1Key).Result()
	if err != nil {
		t.Fatalf("Exists(%s): %v", u1Key, err)
	}
	if existsAfter != 0 {
		t.Fatalf("expected zero-cardinality index %s to be DEL'd from Redis, but key still exists", u1Key)
	}

	t1Key := tenantIndexKey("t-ephemeral-1")
	tenantExists, err := store.rdb.Exists(ctx, t1Key).Result()
	if err != nil {
		t.Fatalf("Exists(%s): %v", t1Key, err)
	}
	if tenantExists != 0 {
		t.Fatalf("expected zero-cardinality tenant index %s to be DEL'd, but key still exists", t1Key)
	}
}

func TestAuditIndex_PerUserCap(t *testing.T) {
	store, _ := setupAudit(t)
	ctx := context.Background()
	// Set global MaxEvents high (5000) so global ts_idx trim does not fire.
	store.cfg.MaxEvents = 5000

	const userCap = 1000
	const extra = 15
	targetUser := "user-heavy"

	for i := 0; i < userCap+extra; i++ {
		_, err := store.record(ctx, RecordInput{
			TenantID: "t-shared",
			UserID:   targetUser,
			Decision: DecisionAllowed,
			Reason:   "rate",
			Handler:  "check",
		})
		if err != nil {
			t.Fatalf("record %d: %v", i+1, err)
		}
	}

	uKey := userIndexKey(targetUser)
	n, err := store.rdb.ZCard(ctx, uKey).Result()
	if err != nil {
		t.Fatalf("ZCard(%s): %v", uKey, err)
	}
	if n != userCap {
		t.Fatalf("expected user index %s cardinality capped at exactly %d, got %d", uKey, userCap, n)
	}
}

// Ghost members (index entries whose event hash already expired) must be
// ZREM'd from user_idx. purge_event cannot HGET them; without the extra ZREM
// the cap loop never shrinks ZCARD and EVAL hangs until lua-time-limit.
func TestAuditIndex_PerUserCapDropsMembersWithoutEventHash(t *testing.T) {
	store, _ := setupAudit(t)
	ctx := context.Background()
	store.cfg.MaxEvents = 5000

	const userCap = 1000
	uid := "user-dangling"
	ukey := userIndexKey(uid)

	for i := 0; i < userCap+1; i++ {
		if err := store.rdb.ZAdd(ctx, ukey, redis.Z{
			Score:  float64(i),
			Member: fmt.Sprintf("ghost-%d", i),
		}).Err(); err != nil {
			t.Fatalf("seed ghost member %d: %v", i, err)
		}
	}

	if _, err := store.record(ctx, RecordInput{
		TenantID: "t-dangling",
		UserID:   uid,
		Decision: DecisionAllowed,
		Reason:   "live",
		Handler:  "check",
	}); err != nil {
		t.Fatalf("record with dangling index members: %v", err)
	}

	n, err := store.rdb.ZCard(ctx, ukey).Result()
	if err != nil {
		t.Fatalf("ZCard(%s): %v", ukey, err)
	}
	if n > userCap {
		t.Fatalf("expected user index capped at <= %d after dangling-hash purge, got %d", userCap, n)
	}
}

func TestAuditEvalKeysShareClusterSlot(t *testing.T) {
	keys := auditEvalKeys("evt-1", "tenant-a", "user-1", "req-1")
	if len(keys) != 5 {
		t.Fatalf("expected 5 EVAL keys, got %d", len(keys))
	}
	if !redisclient.SameClusterSlot(keys...) {
		t.Fatalf("audit EVAL keys must share a hash tag, got %v", keys)
	}
	for _, k := range keys {
		if redisclient.ClusterSlotTag(k) != "audit" {
			t.Fatalf("key %q tag = %q, want audit", k, redisclient.ClusterSlotTag(k))
		}
	}
}
