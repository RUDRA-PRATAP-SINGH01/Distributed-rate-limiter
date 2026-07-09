package limiter

// hierarchical_test.go
// Adversarial correctness tests for HierarchicalLimiter + hierarchical.lua.
//
// Invariants under test:
//   - Happy path: capacity everywhere → allowed, deducts 1 from all levels, returns correct min_remaining.
//   - Rejection at each level independently: if any level fails, allowed=false, NO partial deductions.
//   - All-or-nothing (critical): a globally rejected request consumes 0 tokens at all levels.
//   - Fractional refill per level: independent sub-1.0 rates work without truncation.
//   - Independence: sibling branches (tenant, user, endpoint) do not leak or cross-contaminate.
//   - Global contention: concurrent requests across different paths sharing global key cap N → allowed <= N.
//   - Same-path contention: concurrent requests on same path with single bottleneck cap N → exactly N allowed.
//   - Multi-bottleneck prioritization: tightest bottleneck wins, allowed == bottleneck.
//   - Context/error propagation.

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

// ── helpers ───────────────────────────────────────────────────────────────────

func setupHierarchical(
	t *testing.T,
	gCap, tCap, uCap, eCap int,
	gRate, tRate, uRate, eRate float64,
) (*HierarchicalLimiter, redis.UniversalClient, *miniredis.Miniredis) {
	t.Helper()
	mr, rdb := newMR(t)
	hl := NewHierarchicalLimiter(rdb, gCap, tCap, uCap, eCap, gRate, tRate, uRate, eRate)
	return hl, rdb, mr
}

func allow1H(
	t *testing.T,
	hl *HierarchicalLimiter,
	keys []string,
) (bool, int) {
	t.Helper()
	ok, rem, err := hl.Allow(context.Background(), keys[0], keys[1], keys[2], keys[3])
	if err != nil {
		t.Fatalf("Hierarchical.Allow unexpected error: %v", err)
	}
	return ok, rem
}

// ── Suite 3A: Happy Path ──────────────────────────────────────────────────────

func TestHierarchical_HappyPath_DeductsFromAllLevels(t *testing.T) {
	hl, rdb, _ := setupHierarchical(t, 10, 8, 6, 4, 1.0, 1.0, 1.0, 1.0)
	keys := hierarchyKeys("happy")

	// Verify initial allowance: returns min_remaining.
	// Initial capacities: [10, 8, 6, 4]. Min is 4.
	// After 1 allowed request, remaining tokens should be [9, 7, 5, 3]. Min remaining is 3.
	ok, rem := allow1H(t, hl, keys)
	if !ok {
		t.Fatal("first request must be allowed when all levels have capacity")
	}
	if rem != 3 {
		t.Fatalf("expected remaining (min across levels) = 3, got %d", rem)
	}

	// Verify exact float values in Redis.
	stored := readHierarchyTokens(t, rdb, keys)
	expected := [4]float64{9.0, 7.0, 5.0, 3.0}
	for i := 0; i < 4; i++ {
		if stored[i] != expected[i] {
			t.Errorf("level %d: expected %.1f tokens stored, got %.6f", i, expected[i], stored[i])
		}
		// Verify TTL on keys
		ttl := readTTL(t, rdb, keys[i])
		if ttl <= 0 {
			t.Errorf("level %d key %s missing TTL", i, keys[i])
		}
	}
}

// ── Suite 3B: Rejection at Each Level / All-or-Nothing Invariant ─────────────

func TestHierarchical_AllOrNothing_NoPartialDeductions(t *testing.T) {
	// We run 4 separate scenarios where exactly ONE level is exhausted,
	// and verify that calls are rejected and no other level's tokens are deducted.
	levels := []string{"global", "tenant", "user", "endpoint"}

	for failIdx := 0; failIdx < 4; failIdx++ {
		t.Run("exhaust_"+levels[failIdx], func(t *testing.T) {
			caps := []int{10, 10, 10, 10}
			// Set the target level's capacity to 0, others to 10
			caps[failIdx] = 0

			hl, rdb, _ := setupHierarchical(
				t,
				caps[0], caps[1], caps[2], caps[3],
				0, 0, 0, 0, // no refills
			)
			keys := hierarchyKeys(fmt.Sprintf("aon-%d", failIdx))

			// Prime Redis: we make a call first to make sure keys exist with their
			// respective starting capacities in Redis (since capacity 0 key will start with 0).
			// Let's manually write the initial state to Redis to inspect changes accurately.
			for i := 0; i < 4; i++ {
				rdb.HMSet(context.Background(), keys[i], "tokens", caps[i], "last_refill", time.Now().UnixMilli())
			}

			// Attempt Allow: should be rejected because failIdx has 0 capacity.
			ok, rem := allow1H(t, hl, keys)
			if ok {
				t.Fatal("request should be denied when one level is exhausted")
			}
			if rem != 0 {
				t.Fatalf("expected remaining on denial to be 0, got %d", rem)
			}

			// Critical Invariant: No tokens must be deducted from ANY level.
			// Verified by inspecting tokens in Redis before and after.
			stored := readHierarchyTokens(t, rdb, keys)
			for i := 0; i < 4; i++ {
				expectedVal := float64(caps[i])
				if stored[i] != expectedVal {
					t.Errorf("level %d tokens mutated: expected %.1f, got %.6f. Violation of All-or-Nothing atomicity!", i, expectedVal, stored[i])
				}
			}
		})
	}
}

// ── Suite 3C: Fractional Refill Independently per Level ──────────────────────

func TestHierarchical_FractionalRefill_SubSecond(t *testing.T) {
	// Global: refill 100.0/s
	// Tenant: refill 50.0/s
	// User: refill 200.0/s
	// Endpoint: refill 10.0/s
	hl, rdb, _ := setupHierarchical(t, 10, 10, 10, 10, 100.0, 50.0, 200.0, 10.0)
	keys := hierarchyKeys("frac-refill")

	// Initialize all levels to 0 tokens and the exact same last_refill timestamp
	// to prevent any timing skew from sequential exhaustion.
	now := time.Now().UnixMilli()
	for i := 0; i < 4; i++ {
		rdb.HMSet(context.Background(), keys[i], "tokens", 0, "last_refill", now)
	}

	// Sleep to allow some real-time elapsed.
	time.Sleep(15 * time.Millisecond)

	// Trigger a check to force Lua execution.
	allow1H(t, hl, keys)

	stored := readHierarchyTokens(t, rdb, keys)
	gRefill := stored[0]

	if gRefill <= 0 {
		t.Fatalf("expected positive global refill, got %f", gRefill)
	}

	// Verify the relative ratios of refills are exact.
	expectedTenant := gRefill * 0.5
	expectedUser := gRefill * 2.0
	expectedEndpoint := gRefill * 0.1

	if math.Abs(stored[1]-expectedTenant) > 0.01 {
		t.Errorf("tenant refill ratio wrong: expected %.4f, got %.6f", expectedTenant, stored[1])
	}
	if math.Abs(stored[2]-expectedUser) > 0.01 {
		t.Errorf("user refill ratio wrong: expected %.4f, got %.6f", expectedUser, stored[2])
	}
	if math.Abs(stored[3]-expectedEndpoint) > 0.01 {
		t.Errorf("endpoint refill ratio wrong: expected %.4f, got %.6f", expectedEndpoint, stored[3])
	}
}

// ── Suite 3D: Independence / Sibling Isolation ───────────────────────────────

func TestHierarchical_Independence(t *testing.T) {
	hl, rdb, _ := setupHierarchical(t, 10, 5, 5, 5, 0, 0, 0, 0)

	// Path A and Path B share Global, but have different Tenants/Users/Endpoints.
	globalKey := "hier-global"
	keysA := []string{globalKey, "tenant-a", "user-a", "endpoint-a"}
	keysB := []string{globalKey, "tenant-b", "user-b", "endpoint-b"}

	// Exhaust Path A.
	for i := 0; i < 5; i++ {
		ok, _ := allow1H(t, hl, keysA)
		if !ok {
			t.Fatalf("exhausting path A: request %d should be allowed", i+1)
		}
	}
	// Path A now exhausted (tenant-a, user-a, endpoint-a are at 0).
	okA, _ := allow1H(t, hl, keysA)
	if okA {
		t.Fatal("path A should be exhausted")
	}

	// Path B must still be allowed because its keys (tenant-b, user-b, endpoint-b)
	// are untouched and Global still has 5 tokens left.
	okB, remB := allow1H(t, hl, keysB)
	if !okB {
		t.Fatal("path B should not be blocked by path A exhaustion")
	}
	if remB > 4 { // global remaining is 4, other levels remaining is 4
		t.Fatalf("expected remaining <= 4, got %d", remB)
	}

	_ = rdb
}

// ── Suite 3E: Global Contention ──────────────────────────────────────────────

// ── Suite 3E: Global Contention ──────────────────────────────────────────────

func TestHierarchical_GlobalContention(t *testing.T) {
	const globalCap = 20
	const numIdentities = 50 // more identities than global capacity
	hl, rdb, _ := setupHierarchical(t, globalCap, 10, 10, 10, 0, 0, 0, 0)

	var (
		allowed atomic.Int64
		denied  atomic.Int64
		errs    atomic.Int64
		wg      sync.WaitGroup
		barrier = make(chan struct{})
	)

	for i := 0; i < numIdentities; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			keys := []string{
				"hier:global-contend:global",
				fmt.Sprintf("hier:tenant-%d:tenant", i),
				fmt.Sprintf("hier:user-%d:user", i),
				fmt.Sprintf("hier:endpoint-%d:endpoint", i),
			}
			ok, _, err := hl.Allow(context.Background(), keys[0], keys[1], keys[2], keys[3])
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

	close(barrier)
	wg.Wait()

	gotErrs := errs.Load()
	gotAllowed := allowed.Load()
	gotDenied := denied.Load()

	if gotErrs != 0 {
		t.Errorf("expected 0 errors, got %d", gotErrs)
	}
	if gotAllowed+gotDenied != int64(numIdentities) {
		t.Errorf("expected allowed + denied == total (%d), got %d + %d = %d",
			numIdentities, gotAllowed, gotDenied, gotAllowed+gotDenied)
	}
	if gotAllowed != globalCap {
		t.Errorf("global capacity exceeded: allowed %d requests, global limit is %d", gotAllowed, globalCap)
	}

	// Verify global remaining tokens is exactly 0.
	globalTokens := readHierarchyTokens(t, rdb, []string{"hier:global-contend:global", "non-existent-1", "non-existent-2", "non-existent-3"})
	if globalTokens[0] != 0.0 {
		t.Errorf("expected global remaining tokens to be exactly 0.0, got %f", globalTokens[0])
	}
}

// ── Suite 3F: Same-Path Contention ───────────────────────────────────────────

func TestHierarchical_SamePathContention_BottleneckMatches(t *testing.T) {
	// Bottleneck is User level (cap=5)
	const userBottleneck = 5
	hl, rdb, _ := setupHierarchical(
		t,
		100, 50, userBottleneck, 20,
		0, 0, 0, 0,
	)
	keys := hierarchyKeys("samepath")

	var (
		allowed atomic.Int64
		denied  atomic.Int64
		errs    atomic.Int64
		wg      sync.WaitGroup
		barrier = make(chan struct{})
	)

	const goroutines = 50
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			ok, _, err := hl.Allow(context.Background(), keys[0], keys[1], keys[2], keys[3])
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

	close(barrier)
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
	if gotAllowed != userBottleneck {
		t.Errorf("expected exactly %d allowed (limited by user bottleneck), got %d allowed", userBottleneck, gotAllowed)
	}

	// Verify all levels recorded correctly
	stored := readHierarchyTokens(t, rdb, keys)
	// Deducted amount should be exactly equal to allowed requests (5)
	expectedTokens := [4]float64{95, 45, 0, 15}
	for i := 0; i < 4; i++ {
		if stored[i] != expectedTokens[i] {
			t.Errorf("level %d: expected %.1f tokens remaining, got %.6f", i, expectedTokens[i], stored[i])
		}
	}
}

// ── Suite 3G: Multi-Bottleneck Prioritisation ────────────────────────────────

func TestHierarchical_MultiBottleneck(t *testing.T) {
	// Scenario: Global=100, Tenant=50, User=20, Endpoint=7
	// Expected: Exactly 7 requests allowed on a single path
	hl, rdb, _ := setupHierarchical(t, 100, 50, 20, 7, 0, 0, 0, 0)
	keys := hierarchyKeys("multibottleneck")

	var (
		allowed atomic.Int64
		denied  atomic.Int64
		errs    atomic.Int64
		wg      sync.WaitGroup
		barrier = make(chan struct{})
	)

	const goroutines = 30
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			ok, _, err := hl.Allow(context.Background(), keys[0], keys[1], keys[2], keys[3])
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

	close(barrier)
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
	if gotAllowed != 7 {
		t.Errorf("tightest bottleneck Endpoint(7) should restrict allowed to exactly 7, got %d", gotAllowed)
	}

	// Verify exact deductions
	stored := readHierarchyTokens(t, rdb, keys)
	expectedTokens := [4]float64{93, 43, 13, 0}
	for i := 0; i < 4; i++ {
		if stored[i] != expectedTokens[i] {
			t.Errorf("level %d: expected %.1f tokens, got %.6f", i, expectedTokens[i], stored[i])
		}
	}
}

// ── Suite 3H: Context Errors ──────────────────────────────────────────────────

func TestHierarchical_ContextCancellation(t *testing.T) {
	hl, _, _ := setupHierarchical(t, 10, 10, 10, 10, 1.0, 1.0, 1.0, 1.0)
	keys := hierarchyKeys("ctx")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel context immediately

	ok, _, err := hl.AllowWithParams(ctx, keys, []int{10, 10, 10, 10}, []float64{1, 1, 1, 1})
	if err == nil {
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
