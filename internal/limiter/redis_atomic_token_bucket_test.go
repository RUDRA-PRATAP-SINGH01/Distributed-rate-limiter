package limiter

// redis_atomic_token_bucket_test.go
// Adversarial correctness tests for RedisAtomicTokenBucket + token_bucket.lua.
//
// Invariants under test:
//   - Init: fresh key → full capacity; first request deducts exactly 1.
//   - Exact exhaustion: exactly N allowed, N+1 denied, remaining never negative.
//   - Fractional refill: sub-1.0/s rates preserve fractional tokens in Redis (no floor truncation).
//   - Millisecond precision: tokens accumulate on sub-second elapsed times.
//   - Capacity cap: tokens never exceed capacity after idle.
//   - Rejection semantics: denied request deducts zero; no negative tokens; last_refill updated.
//   - Key isolation: exhausting key A does not affect key B.
//   - Atomicity: capacity=N, N+M goroutines → exactly N allowed.
//   - Invalid config: zero/negative values silently accepted (reported as finding).
//   - Context cancellation: returns error, no panic.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// newTB returns a fresh atomic token bucket and its miniredis state for
// direct inspection.
func newTB(t *testing.T, cap int, rate float64) (*RedisAtomicTokenBucket, redis.UniversalClient, *miniredis.Miniredis) {
	t.Helper()
	mr, rdb := newMR(t)
	tb := NewRedisAtomicTokenBucket(rdb, cap, rate)
	return tb, rdb, mr
}

// allow1 calls Allow and fails immediately if an unexpected error occurs.
func allow1(t *testing.T, tb *RedisAtomicTokenBucket, uid string) (bool, int) {
	t.Helper()
	ok, rem, err := tb.Allow(context.Background(), uid)
	if err != nil {
		t.Fatalf("Allow(%s): unexpected error: %v", uid, err)
	}
	return ok, rem
}

// ── Suite 1A: Initialisation ─────────────────────────────────────────────────

func TestTokenBucket_Init_FreshKeyFullCapacity(t *testing.T) {
	for _, cap := range []int{1, 5, 100} {
		t.Run(fmt.Sprintf("cap%d", cap), func(t *testing.T) {
			tb, rdb, _ := newTB(t, cap, 1.0)
			uid := fmt.Sprintf("init-fresh-%d", cap)

			// Before first call: key must not exist.
			if keyExists(t, rdb, "rate:"+uid) {
				t.Fatal("key should not exist before first Allow")
			}

			ok, rem := allow1(t, tb, uid)
			if !ok {
				t.Fatalf("first request must be allowed (cap=%d)", cap)
			}
			if rem != cap-1 {
				t.Fatalf("remaining after first deduction: got %d, want %d", rem, cap-1)
			}

			// Redis state: tokens field must be written, TTL must exist.
			tokStr, _ := readTokenBucketState(t, rdb, uid)
			if tokStr == "" {
				t.Fatal("tokens field not written to Redis after Allow")
			}
			ttl := readTTL(t, rdb, "rate:"+uid)
			if ttl <= 0 {
				t.Fatalf("expected positive TTL, got %d", ttl)
			}
		})
	}
}

// ── Suite 1B: Exact Exhaustion ────────────────────────────────────────────────

func TestTokenBucket_ExactExhaustion(t *testing.T) {
	cases := []struct {
		cap  int
		rate float64 // set very low so no refill during the test
	}{
		{1, 0.0001},
		{2, 0.0001},
		{10, 0.0001},
		{50, 0.0001},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("cap%d", tc.cap), func(t *testing.T) {
			tb, rdb, _ := newTB(t, tc.cap, tc.rate)
			uid := fmt.Sprintf("exhaust-%d", tc.cap)

			// Exhaust exactly cap tokens.
			for i := 0; i < tc.cap; i++ {
				ok, rem := allow1(t, tb, uid)
				if !ok {
					t.Fatalf("request %d of %d denied (expected allowed)", i+1, tc.cap)
				}
				if rem < 0 {
					t.Fatalf("remaining went negative at request %d: %d", i+1, rem)
				}
			}

			// N+1 must be denied.
			ok, rem := allow1(t, tb, uid)
			if ok {
				t.Fatalf("request %d should be denied after exhaustion (cap=%d)", tc.cap+1, tc.cap)
			}
			if rem < 0 {
				t.Fatalf("remaining must not be negative on denial, got %d", rem)
			}

			// Persisted tokens must never be negative.
			stored := readTokensFloat(t, rdb, uid)
			if stored < 0 {
				t.Fatalf("persisted tokens are negative: %f", stored)
			}
		})
	}
}

// ── Suite 1C: Fractional Refill (Critical Regression) ────────────────────────
//
// Algorithm: token_bucket.lua stores tokens as a float.
// Elapsed time is (now - last_refill) / 1000.0 (ms → seconds).
// floor() is applied only for the comparison (math.floor(new_tokens) >= requested)
// and for the returned 'remaining' — NOT to the stored value.
//
// This test verifies that the stored float survives repeated Allow() calls
// when refill_rate < 1 token/s, so accumulated fractional progress is not lost.

func TestTokenBucket_FractionalRefill_PreservesSubTokenState(t *testing.T) {
	cases := []struct {
		rate float64
	}{
		{0.1},
		{0.5},
		{1.5},
		{0.333},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("rate%.3f", tc.rate), func(t *testing.T) {
			ctx := context.Background()
			tb, rdb, _ := newTB(t, 5, tc.rate)
			uid := fmt.Sprintf("frac-preserve-%.3f", tc.rate)
			key := "rate:" + uid

			const stepMs = 50
			for step := 1; step <= 3; step++ {
				// Seed 0 tokens and last_refill in the past.
				currentTime := time.Now().UnixMilli()
				if err := rdb.HMSet(ctx, key, "tokens", 0.0, "last_refill", currentTime-stepMs).Err(); err != nil {
					t.Fatalf("failed to seed: %v", err)
				}

				ok, rem, err := tb.Allow(ctx, uid)
				if err != nil {
					t.Fatalf("Allow: %v", err)
				}
				if ok {
					t.Fatalf("request must be denied as accumulated tokens < 1.0")
				}
				if rem != 0 {
					t.Fatalf("remaining must be 0, got %d", rem)
				}

				stored := readTokensFloat(t, rdb, uid)
				// The stored tokens should be approximately stepMs * rate / 1000.0
				minExpected := float64(stepMs) * tc.rate / 1000.0
				maxExpected := float64(stepMs+8) * tc.rate / 1000.0 // Allow small execution time delta
				if stored < minExpected || stored > maxExpected {
					t.Fatalf("step %d: stored tokens %.6f outside expected range [%.6f, %.6f]", step, stored, minExpected, maxExpected)
				}
			}
		})
	}
}

func TestTokenBucket_FractionalRefill_EventuallyAllows(t *testing.T) {
	cases := []struct {
		rate float64
	}{
		{0.1},
		{0.5},
		{1.5},
		{0.333},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("rate%.3f", tc.rate), func(t *testing.T) {
			ctx := context.Background()
			tb, rdb, _ := newTB(t, 5, tc.rate)
			uid := fmt.Sprintf("frac-allow-%.3f", tc.rate)
			key := "rate:" + uid

			// Seed to 0.9 tokens, so < 1.0 (denied)
			currentTime := time.Now().UnixMilli()
			if err := rdb.HMSet(ctx, key, "tokens", 0.9, "last_refill", currentTime).Err(); err != nil {
				t.Fatalf("failed to seed: %v", err)
			}

			ok1, _, err := tb.Allow(ctx, uid)
			if err != nil {
				t.Fatalf("Allow: %v", err)
			}
			if ok1 {
				t.Fatalf("0.9 tokens must be denied")
			}

			// We need 0.1 tokens more.
			requiredMs := int64(math.Ceil(0.1/tc.rate*1000.0)) + 5 // add 5ms safety buffer
			currentTime = time.Now().UnixMilli()

			// Seed last_refill in the past by requiredMs.
			if err := rdb.HMSet(ctx, key, "tokens", 0.9, "last_refill", currentTime-requiredMs).Err(); err != nil {
				t.Fatalf("failed to seed: %v", err)
			}

			ok2, rem, err := tb.Allow(ctx, uid)
			if err != nil {
				t.Fatalf("Allow: %v", err)
			}
			if !ok2 {
				t.Fatalf("expected allowed after sufficient simulated time elapsed")
			}
			if rem < 0 {
				t.Fatalf("remaining cannot be negative: %d", rem)
			}
		})
	}
}

// ── Suite 1D: Millisecond Precision ──────────────────────────────────────────
// Demonstrate that a sub-second fast-forward genuinely accumulates tokens.
// At refillRate=10.0, 50ms -> +0.5 tokens; 150ms -> +1.5 tokens (allows a request).

func TestTokenBucket_MillisecondPrecision(t *testing.T) {
	ctx := context.Background()
	tb, rdb, _ := newTB(t, 10, 10.0) // 10 tokens/s
	uid := "ms-precision"
	key := "rate:" + uid

	// Seed 0 tokens, last_refill at currentTime - 50ms.
	currentTime := time.Now().UnixMilli()
	if err := rdb.HMSet(ctx, key, "tokens", 0.0, "last_refill", currentTime-50).Err(); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	ok, _, err := tb.Allow(ctx, uid)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if ok {
		t.Fatalf("50ms at 10tok/s should refill only ~0.5 tokens and must be denied")
	}

	stored := readTokensFloat(t, rdb, uid)
	// Stored must be approximately 0.5 (range [0.5, 0.58])
	if stored < 0.50 || stored > 0.58 {
		t.Fatalf("expected stored tokens in range [0.50, 0.58], got %.6f", stored)
	}

	// Now seed 0 tokens, last_refill at currentTime - 150ms.
	currentTime = time.Now().UnixMilli()
	if err := rdb.HMSet(ctx, key, "tokens", 0.0, "last_refill", currentTime-150).Err(); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	ok, rem, err := tb.Allow(ctx, uid)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !ok {
		t.Fatalf("150ms at 10tok/s should refill ~1.5 tokens and must be allowed")
	}
	if rem < 0 {
		t.Fatalf("remaining cannot be negative: %d", rem)
	}
}

// ── Suite 1E: Capacity Cap ────────────────────────────────────────────────────
// After long idle, tokens must never exceed capacity.

func TestTokenBucket_CapacityNeverExceeded(t *testing.T) {
	_, rdb := newMR(t)
	tb := NewRedisAtomicTokenBucket(rdb, 5, 1000.0) // 1000 tokens/s, cap 5
	uid := "cap-limit"

	// Prime the bucket by making one call.
	allow1(t, tb, uid)

	// Sleep 15ms — would refill 15 tokens.
	time.Sleep(15 * time.Millisecond)
	ok, rem := allow1(t, tb, uid)
	if !ok {
		t.Fatal("after long idle must be allowed")
	}
	if rem > 4 { // cap-1 after deduction
		t.Fatalf("remaining > capacity-1: %d", rem)
	}

	stored := readTokensFloat(t, rdb, uid)
	if stored > 5.0 {
		t.Fatalf("stored tokens %.4f exceed capacity 5", stored)
	}
}

// ── Suite 1F: Rejection Semantics ────────────────────────────────────────────
// A denied request must not deduct any tokens from the bucket.

func TestTokenBucket_RejectionDeductsNothing(t *testing.T) {
	ctx := context.Background()
	tb, rdb, _ := newTB(t, 3, 0.0) // exact 0 refill rate
	uid := "rejection-deduct"
	key := "rate:" + uid

	// Seed exactly 0.0 tokens and last_refill 100ms in the past.
	now := time.Now().UnixMilli()
	if err := rdb.HMSet(ctx, key, "tokens", 0.0, "last_refill", now-100).Err(); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	// Capture state before rejections.
	beforeStr, beforeRefillStr := readTokenBucketState(t, rdb, uid)

	// Issue 5 denials.
	for i := 0; i < 5; i++ {
		ok, rem, err := tb.Allow(ctx, uid)
		if err != nil {
			t.Fatalf("Allow failed: %v", err)
		}
		if ok {
			t.Fatalf("request %d should be denied", i+1)
		}
		if rem != 0 {
			t.Fatalf("remaining must be 0, got %d", rem)
		}
	}

	// Capture state after rejections.
	afterStr, afterRefillStr := readTokenBucketState(t, rdb, uid)

	// Assert stored balance is exactly unchanged.
	if beforeStr != afterStr {
		t.Fatalf("tokens mutated on denial: before=%q, after=%q", beforeStr, afterStr)
	}
	// Assert last_refill was updated to the new timestamps.
	if beforeRefillStr == afterRefillStr {
		t.Fatalf("last_refill must be updated on denial: got=%q", afterRefillStr)
	}
}

// ── Suite 1G: Key Isolation ───────────────────────────────────────────────────

func TestTokenBucket_KeyIsolation(t *testing.T) {
	_, rdb := newMR(t)
	tb := NewRedisAtomicTokenBucket(rdb, 2, 0.0001)

	uidA := "isolation-a"
	uidB := "isolation-b"

	// Exhaust A.
	allow1(t, tb, uidA)
	allow1(t, tb, uidA)

	// A must now be denied.
	okA, _ := allow1(t, tb, uidA)
	if okA {
		t.Fatal("A should be exhausted")
	}

	// B must still be allowed for 2 requests.
	okB1, _ := allow1(t, tb, uidB)
	okB2, _ := allow1(t, tb, uidB)
	if !okB1 || !okB2 {
		t.Fatal("B should be independent and have full capacity")
	}
}

// ── Suite 1H: Atomicity / Concurrency ────────────────────────────────────────
// capacity=N, refill disabled, ≫N goroutines → exactly N allowed.
// This tests Redis-side logical oversubscription (not Go memory race).

func runAtomicConcurrencyCheck(t *testing.T, capacity, goroutines int) {
	t.Helper()
	_, rdb, _ := newTB(t, capacity, 0) // 0 refill
	tb := NewRedisAtomicTokenBucket(rdb, capacity, 0)

	var (
		allowed atomic.Int64
		denied  atomic.Int64
		errs    atomic.Int64
		wg      sync.WaitGroup
		barrier = make(chan struct{})
	)

	uid := fmt.Sprintf("atomic-cap%d-g%d", capacity, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			ok, _, err := tb.Allow(context.Background(), uid)
			if err != nil {
				errs.Add(1)
				return
			}
			if ok {
				allowed.Add(1)
			} else {
				denied.Add(1)
			}
		}()
	}

	close(barrier) // release all goroutines simultaneously
	wg.Wait()

	gotErrs := errs.Load()
	gotAllowed := allowed.Load()
	gotDenied := denied.Load()

	if gotErrs != 0 {
		t.Errorf("expected 0 errors, got %d", gotErrs)
	}
	if gotAllowed+gotDenied != int64(goroutines) {
		t.Errorf("expected allowed + denied == total (%d), got %d + %d = %d",
			goroutines, gotAllowed, gotDenied, gotAllowed+gotDenied)
	}
	if gotAllowed != int64(capacity) {
		t.Errorf("goroutines=%d, cap=%d: exactly %d should be allowed, got %d allowed, %d denied",
			goroutines, capacity, capacity, gotAllowed, gotDenied)
	}

	// Redis state must have non-negative tokens.
	stored := readTokensFloat(t, rdb, uid)
	if stored < 0 {
		t.Errorf("persisted tokens negative after concurrency test: %f", stored)
	}
}

func TestTokenBucket_Atomicity_30Goroutines(t *testing.T) {
	// Run several times to catch non-deterministic races.
	for i := 0; i < 3; i++ {
		t.Run(fmt.Sprintf("run%d", i), func(t *testing.T) {
			runAtomicConcurrencyCheck(t, 10, 30)
		})
	}
}

func TestTokenBucket_Atomicity_50Goroutines(t *testing.T) {
	for i := 0; i < 3; i++ {
		t.Run(fmt.Sprintf("run%d", i), func(t *testing.T) {
			runAtomicConcurrencyCheck(t, 20, 50)
		})
	}
}

// ── Suite 1I: Multi-Key Concurrency ──────────────────────────────────────────
// Concurrent traffic across many keys: each key respects its own capacity.

func TestTokenBucket_MultiKeyConcurrency(t *testing.T) {
	const numKeys = 10
	const cap = 5
	const goroutinesPerKey = 20

	_, rdb := newMR(t)
	tb := NewRedisAtomicTokenBucket(rdb, cap, 0)

	results := make([]atomic.Int64, numKeys)
	errorsList := make([]atomic.Int64, numKeys)
	deniedList := make([]atomic.Int64, numKeys)
	var wg sync.WaitGroup
	barrier := make(chan struct{})

	for k := 0; k < numKeys; k++ {
		uid := fmt.Sprintf("multikey-%d", k)
		for i := 0; i < goroutinesPerKey; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-barrier
				ok, _, err := tb.Allow(context.Background(), uid)
				if err != nil {
					errorsList[k].Add(1)
					return
				}
				if ok {
					results[k].Add(1)
				} else {
					deniedList[k].Add(1)
				}
			}()
		}
	}

	close(barrier)
	wg.Wait()

	for k := 0; k < numKeys; k++ {
		gotErrs := errorsList[k].Load()
		gotAllowed := results[k].Load()
		gotDenied := deniedList[k].Load()

		if gotErrs != 0 {
			t.Errorf("key %d: expected 0 errors, got %d", k, gotErrs)
		}
		if gotAllowed+gotDenied != int64(goroutinesPerKey) {
			t.Errorf("key %d: expected allowed + denied == total (%d), got %d + %d = %d",
				k, goroutinesPerKey, gotAllowed, gotDenied, gotAllowed+gotDenied)
		}
		if gotAllowed != cap {
			t.Errorf("key %d: expected exactly %d allowed, got %d", k, cap, gotAllowed)
		}
	}
}

// ── Suite 1J: Invalid / Edge Config ──────────────────────────────────────────
// FINDING REPORT: The constructor does not validate inputs — zero/negative
// capacity and rate are silently accepted. Tests document actual behavior.

func TestTokenBucket_ZeroCapacity_Finding(t *testing.T) {
	_, rdb := newMR(t)
	tb := NewRedisAtomicTokenBucket(rdb, 0, 1.0)
	uid := "zero-cap"

	// With capacity=0, first call should be denied (0 tokens available).
	// Document actual behavior — do not assert a specific error from constructor.
	ok, rem, err := tb.Allow(context.Background(), uid)
	t.Logf("FINDING: capacity=0 — Allow returned ok=%v, remaining=%d, err=%v", ok, rem, err)

	// Invariant: remaining must never be negative.
	if err == nil && rem < 0 {
		t.Errorf("remaining is negative (%d) with zero capacity — invariant violation", rem)
	}
}

func TestTokenBucket_ZeroRefillRate(t *testing.T) {
	_, rdb := newMR(t)
	// Exact 0 refill: bucket starts full but never refills.
	tb := NewRedisAtomicTokenBucket(rdb, 3, 0)
	uid := "zero-refill"

	ok1, _ := allow1(t, tb, uid)
	ok2, _ := allow1(t, tb, uid)
	ok3, _ := allow1(t, tb, uid)
	if !ok1 || !ok2 || !ok3 {
		t.Fatal("first 3 requests with zero refill should be allowed from initial capacity")
	}
	ok4, rem := allow1(t, tb, uid)
	if ok4 {
		t.Fatal("4th request with zero refill must be denied")
	}
	if rem != 0 {
		t.Fatalf("remaining after exhaustion with zero refill: got %d, want 0", rem)
	}
}

func TestTokenBucket_VeryLargeCapacity(t *testing.T) {
	_, rdb := newMR(t)
	tb := NewRedisAtomicTokenBucket(rdb, 1_000_000, 0)
	uid := "large-cap"

	ok, rem := allow1(t, tb, uid)
	if !ok {
		t.Fatal("large capacity: first request must be allowed")
	}
	if rem != 999_999 {
		t.Fatalf("large capacity: remaining want 999999, got %d", rem)
	}
}

// ── Suite 1K: Context Cancellation ───────────────────────────────────────────

func TestTokenBucket_CancelledContext(t *testing.T) {
	_, rdb := newMR(t)
	tb := NewRedisAtomicTokenBucket(rdb, 10, 1.0)
	uid := "ctx-cancel"

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling Allow

	ok, _, err := tb.Allow(ctx, uid)
	if err == nil {
		// Miniredis may succeed if context check isn't enforced in mocks.
		// We document the warning/divergence.
		if isRealRedis {
			t.Fatal("real Redis: cancelled context must return error")
		}
		t.Logf("NOTE: cancelled context did not return error under miniredis; ok=%v", ok)
	} else {
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled error, got %v", err)
		}
	}
}

func TestTokenBucket_DeadlineExceeded(t *testing.T) {
	_, rdb := newMR(t)
	tb := NewRedisAtomicTokenBucket(rdb, 10, 1.0)
	uid := "ctx-deadline"

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel()

	_, _, err := tb.Allow(ctx, uid)
	if err == nil {
		if isRealRedis {
			t.Fatal("real Redis: expired deadline must return error")
		}
		t.Logf("NOTE: expired deadline did not return error under miniredis")
	} else {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected context.DeadlineExceeded error, got %v", err)
		}
	}
}

// ── Suite 1L: Property Invariant — 0 ≤ stored_tokens ≤ capacity ──────────────

func TestTokenBucket_PropertyStoredTokensBounded(t *testing.T) {
	_, rdb := newMR(t)
	const cap = 8
	tb := NewRedisAtomicTokenBucket(rdb, cap, 50.0) // 50 tokens/s
	uid := "prop-bounded"

	// Run iterations with time advances in between.
	for step := 0; step < 5; step++ {
		time.Sleep(20 * time.Millisecond)
		tb.Allow(context.Background(), uid) //nolint:errcheck
		stored := readTokensFloat(t, rdb, uid)
		if stored < 0 || stored > float64(cap) {
			t.Fatalf("step %d: invariant violated: stored=%.6f not in [0, %d]", step, stored, cap)
		}
	}
}

// ── Suite 1M: Server Time Source (M-01) ──────────────────────────────────────
// Redis TIME must drive token refill, independent of Go process wall-clock.

func TestTokenBucket_UsesRedisTimeNotClientClock(t *testing.T) {
	if isRealRedis {
		t.Skip("SetTime not available on real Redis")
	}
	// Use an isolated miniredis instance to avoid mutating the shared clock.
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mr.SetTime(t0)

	tb := NewRedisAtomicTokenBucket(rdb, 5, 1.0) // 5 capacity, 1.0 token/s
	uid := "redis-time-test"

	// Exhaust all 5 tokens at t0.
	for i := 0; i < 5; i++ {
		ok, _, err := tb.Allow(context.Background(), uid)
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
		if !ok {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	ok, _, err := tb.Allow(context.Background(), uid)
	if err != nil {
		t.Fatalf("6th request: %v", err)
	}
	if ok {
		t.Fatal("6th request should be denied (bucket exhausted)")
	}

	// Advance Redis time by 3 seconds — exactly 3 tokens should refill.
	mr.SetTime(t0.Add(3 * time.Second))

	for i := 0; i < 3; i++ {
		ok, _, err := tb.Allow(context.Background(), uid)
		if err != nil {
			t.Fatalf("post-refill request %d: %v", i+1, err)
		}
		if !ok {
			t.Fatalf("post-refill request %d should be allowed", i+1)
		}
	}
	ok, _, err = tb.Allow(context.Background(), uid)
	if err != nil {
		t.Fatalf("4th post-refill request: %v", err)
	}
	if ok {
		t.Fatal("4th post-refill request should be denied (only 3 refilled)")
	}
}
