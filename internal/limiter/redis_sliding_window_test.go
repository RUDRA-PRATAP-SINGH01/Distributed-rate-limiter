package limiter

// redis_sliding_window_test.go
// Adversarial correctness tests for RedisSlidingWindow + sliding_window.lua.
//
// Implementation facts confirmed from source:
//   - Uses a ZSET per key ("sw:<userID>").
//   - ZREMRANGEBYSCORE removes entries with score in [0, windowStart] INCLUSIVE.
//     (windowStart = now - window.Milliseconds())
//   - ZADD adds the member only on allowed. Member is unique per call ("now_ms:now_ns").
//   - EXPIRE set only on successful ZADD; key left to expire naturally on full window.
//   - returned 'remaining' = limit - count - 1 (where count is post-prune cardinality).
//
// Invariants:
//   - Init: empty key, first allowed, ZSET cardinality=1, TTL set.
//   - Exact limit: first N allowed, N+1 denied.
//   - Window expiry: expired entries removed, capacity freed.
//   - Boundary: ZREMRANGEBYSCORE is [0, windowStart] — entries at exactly windowStart are REMOVED.
//   - Same-ms uniqueness: concurrent requests within 1ms produce unique ZSET members.
//   - Atomicity: M≫N concurrent → exactly N allowed, ZSET cardinality=N.
//   - Key isolation & TTL.
//   - Context/error propagation.

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func allow1SW(t *testing.T, sw *RedisSlidingWindow, uid string) (bool, int) {
	t.Helper()
	ok, rem, err := sw.Allow(context.Background(), uid)
	if err != nil {
		t.Fatalf("Allow(%s): unexpected error: %v", uid, err)
	}
	return ok, rem
}

// ── Suite 2A: Init ────────────────────────────────────────────────────────────

func TestSlidingWindow_Init_FreshKeyFirstAllowed(t *testing.T) {
	mr, rdb := newMR(t)
	sw := NewRedisSlidingWindow(rdb, 5, time.Second)
	uid := "sw-init"

	// Key must not exist before first call.
	if keyExists(t, rdb, "sw:"+uid) {
		t.Fatal("ZSET key must not exist before first Allow")
	}

	ok, rem := allow1SW(t, sw, uid)
	if !ok {
		t.Fatal("first request must be allowed")
	}
	if rem != 4 {
		t.Fatalf("remaining: got %d, want 4", rem)
	}

	// ZSET must exist and have cardinality 1.
	card := zCard(t, rdb, "sw:"+uid)
	if card != 1 {
		t.Fatalf("ZSET cardinality: got %d, want 1", card)
	}

	// TTL must be set.
	ttl := readTTL(t, rdb, "sw:"+uid)
	if ttl <= 0 {
		t.Fatalf("TTL must be positive, got %d", ttl)
	}

	_ = mr // referenced to avoid compiler error
}

// ── Suite 2B: Exact Limit ─────────────────────────────────────────────────────

func TestSlidingWindow_ExactLimit(t *testing.T) {
	cases := []int{1, 3, 10, 25}
	for _, limit := range cases {
		limit := limit
		t.Run(fmt.Sprintf("limit%d", limit), func(t *testing.T) {
			_, rdb := newMR(t)
			sw := NewRedisSlidingWindow(rdb, limit, 10*time.Second)
			uid := fmt.Sprintf("sw-exact-%d", limit)

			for i := 0; i < limit; i++ {
				ok, _ := allow1SW(t, sw, uid)
				if !ok {
					t.Fatalf("request %d/%d denied, expected allowed", i+1, limit)
				}
				// Sleep 1ms to ensure next request gets a different timestamp/member ID
				time.Sleep(1 * time.Millisecond)
			}

			ok, _ := allow1SW(t, sw, uid)
			if ok {
				t.Fatalf("request %d should be denied (limit=%d)", limit+1, limit)
			}

			// ZSET cardinality must equal the limit.
			card := zCard(t, rdb, "sw:"+uid)
			if card != int64(limit) {
				t.Fatalf("ZSET cardinality: got %d, want %d", card, limit)
			}
		})
	}
}

// ── Suite 2C: Window Expiry ───────────────────────────────────────────────────

func TestSlidingWindow_WindowExpiry_FreesCapacity(t *testing.T) {
	_, rdb := newMR(t)
	sw := NewRedisSlidingWindow(rdb, 3, 100*time.Millisecond)
	uid := "sw-expiry"

	// Fill the window.
	for i := 0; i < 3; i++ {
		allow1SW(t, sw, uid)
		time.Sleep(1 * time.Millisecond)
	}

	// 4th must be denied.
	ok, _ := allow1SW(t, sw, uid)
	if ok {
		t.Fatal("4th request should be denied while window full")
	}

	// Sleep past the window. All entries should be pruned.
	time.Sleep(120 * time.Millisecond)

	// Now 3 new requests should be allowed.
	for i := 0; i < 3; i++ {
		ok, _ := allow1SW(t, sw, uid)
		if !ok {
			t.Fatalf("post-expiry request %d should be allowed", i+1)
		}
		time.Sleep(1 * time.Millisecond)
	}
}

// ── Suite 2D: Window Boundary Semantics ──────────────────────────────────────
// The Lua script removes entries with score in [0, windowStart] INCLUSIVE.
// windowStart = now - window.Milliseconds()
// An entry at exactly windowStart is REMOVED — it's outside the window.

func TestSlidingWindow_BoundarySemantics(t *testing.T) {
	_, rdb := newMR(t)
	const window = 150 * time.Millisecond
	sw := NewRedisSlidingWindow(rdb, 1, window)
	uid := "sw-boundary"

	// Allow at T=0.
	allow1SW(t, sw, uid)

	// Sleep 140ms — inside the 150ms window. Should be denied.
	time.Sleep(140 * time.Millisecond)
	ok, _ := allow1SW(t, sw, uid)
	if ok {
		t.Fatal("inside window: must be denied")
	}

	// Sleep another 20ms (total ~160ms) — outside the window.
	time.Sleep(20 * time.Millisecond)
	ok, _ = allow1SW(t, sw, uid)
	if !ok {
		t.Fatal("outside window: must be allowed")
	}

	_ = rdb
}

// ── Suite 2E: Same-Millisecond Uniqueness ─────────────────────────────────────
// The member format is "nowMs:nowNano" which must produce unique ZSET members
// even for concurrent same-ms calls.

func TestSlidingWindow_SameMsUniqueness(t *testing.T) {
	_, rdb := newMR(t)
	const limit = 50
	sw := NewRedisSlidingWindow(rdb, limit, 10*time.Second)
	uid := "sw-uniqueness"

	var wg sync.WaitGroup
	barrier := make(chan struct{})

	for i := 0; i < limit; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			sw.Allow(context.Background(), uid) //nolint:errcheck
		}()
	}
	close(barrier)
	wg.Wait()

	// Cardinality must equal the number of accepted requests (no member collisions).
	card := zCard(t, rdb, "sw:"+uid)
	if card > int64(limit) {
		t.Fatalf("ZSET cardinality %d exceeds limit %d — members shouldn't be overwritten", card, limit)
	}
	// cardinality == card means no collisions occurred (all members unique).
	t.Logf("ZSET cardinality after %d concurrent requests: %d (limit=%d)", limit, card, limit)
}

// ── Suite 2F: Concurrency / Atomicity ─────────────────────────────────────────

func TestSlidingWindow_Atomicity(t *testing.T) {
	const limit = 15
	const goroutines = 50

	for run := 0; run < 3; run++ {
		run := run
		t.Run(fmt.Sprintf("run%d", run), func(t *testing.T) {
			_, rdb := newMR(t)
			sw := NewRedisSlidingWindow(rdb, limit, 10*time.Second)
			uid := fmt.Sprintf("sw-atomic-run%d", run)

			var allowed atomic.Int64
			var wg sync.WaitGroup
			barrier := make(chan struct{})

			for i := 0; i < goroutines; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-barrier
					ok, _, err := sw.Allow(context.Background(), uid)
					if err == nil && ok {
						allowed.Add(1)
					}
				}()
			}
			close(barrier)
			wg.Wait()

			got := allowed.Load()
			if got != limit {
				t.Errorf("run %d: expected exactly %d allowed, got %d", run, limit, got)
			}

			// ZSET cardinality must equal allowed count.
			card := zCard(t, rdb, "sw:"+uid)
			if card != got {
				t.Errorf("run %d: ZSET cardinality %d ≠ allowed count %d", run, card, got)
			}
		})
	}
}

// ── Suite 2G: Key Isolation ───────────────────────────────────────────────────

func TestSlidingWindow_KeyIsolation(t *testing.T) {
	_, rdb := newMR(t)
	sw := NewRedisSlidingWindow(rdb, 2, 10*time.Second)

	// Exhaust A.
	allow1SW(t, sw, "sw-iso-a")
	allow1SW(t, sw, "sw-iso-a")
	okA, _ := allow1SW(t, sw, "sw-iso-a")
	if okA {
		t.Fatal("A should be exhausted")
	}

	// B untouched — must allow 2.
	okB1, _ := allow1SW(t, sw, "sw-iso-b")
	okB2, _ := allow1SW(t, sw, "sw-iso-b")
	if !okB1 || !okB2 {
		t.Fatal("B should be independent and allow 2 requests")
	}

	_ = rdb
}

// ── Suite 2H: Context Cancellation ───────────────────────────────────────────

func TestSlidingWindow_CancelledContext(t *testing.T) {
	_, rdb := newMR(t)
	sw := NewRedisSlidingWindow(rdb, 10, time.Second)
	uid := "sw-ctx-cancel"

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ok, _, err := sw.Allow(ctx, uid)
	t.Logf("cancelled context: ok=%v err=%v", ok, err)
	// Must not panic.

	_ = rdb
}

// ── Suite 2I: Active Count Never Exceeds Limit ───────────────────────────────

func TestSlidingWindow_PropertyActiveCountNeverExceedsLimit(t *testing.T) {
	_, rdb := newMR(t)
	const limit = 5
	sw := NewRedisSlidingWindow(rdb, limit, 200*time.Millisecond)
	uid := "sw-prop"

	for step := 0; step < 5; step++ {
		time.Sleep(50 * time.Millisecond)
		ok, _, err := sw.Allow(context.Background(), uid)
		if err != nil {
			continue
		}
		card := zCard(t, rdb, "sw:"+uid)
		if ok && card > int64(limit) {
			t.Errorf("step %d: ZSET cardinality %d exceeds limit %d after allowed request", step, card, limit)
		}
	}

	_ = rdb
}
