package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/identity"
)

// Test Suite 7: Denial cache
func TestSidecar_DenialCache(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 50*time.Millisecond, false)
	defer cleanup()

	// Initial denial
	fixture.limiterHandler.allowed = false
	fixture.limiterHandler.limit = 5
	fixture.limiterHandler.remaining = 0

	req1 := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req1.Header.Set(identity.UserIDHeader, "user-denied-cache")
	rr1 := httptest.NewRecorder()
	fixture.sidecar.ServeHTTP(rr1, req1)

	if rr1.Result().StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected HTTP 429, got %d", rr1.Result().StatusCode)
	}
	if fixture.limiterHandler.callCount != 1 {
		t.Fatalf("expected 1 call to central limiter, got %d", fixture.limiterHandler.callCount)
	}

	// Repeated identical request within TTL -> should serve from cache
	req2 := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req2.Header.Set(identity.UserIDHeader, "user-denied-cache")
	rr2 := httptest.NewRecorder()
	fixture.sidecar.ServeHTTP(rr2, req2)

	if rr2.Result().StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected cached denial to return HTTP 429, got %d", rr2.Result().StatusCode)
	}
	if fixture.limiterHandler.callCount != 1 {
		t.Fatalf("expected limiter call count unchanged (1), got %d (served from cache)", fixture.limiterHandler.callCount)
	}

	// Wait past TTL expiry (50ms)
	time.Sleep(70 * time.Millisecond)

	// Post-expiry request -> should query central limiter again
	req3 := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req3.Header.Set(identity.UserIDHeader, "user-denied-cache")
	rr3 := httptest.NewRecorder()
	fixture.sidecar.ServeHTTP(rr3, req3)

	if rr3.Result().StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected post-expiry HTTP 429, got %d", rr3.Result().StatusCode)
	}
	if fixture.limiterHandler.callCount != 2 {
		t.Fatalf("expected limiter called again after expiry, got count %d", fixture.limiterHandler.callCount)
	}
}

// Test Suite 8: Allowance cache (verify it is not implemented for allowances)
func TestSidecar_AllowanceCache(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 50*time.Millisecond, false)
	defer cleanup()

	// Rate limiter allows
	fixture.limiterHandler.allowed = true

	// Call 1
	req1 := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req1.Header.Set(identity.UserIDHeader, "user-allowed-cache")
	rr1 := httptest.NewRecorder()
	fixture.sidecar.ServeHTTP(rr1, req1)

	if rr1.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", rr1.Result().StatusCode)
	}
	if fixture.limiterHandler.callCount != 1 {
		t.Fatalf("expected 1 call to central limiter, got %d", fixture.limiterHandler.callCount)
	}

	// Call 2 (identical request, within TTL) -> should NOT serve from cache and call limiter again
	req2 := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req2.Header.Set(identity.UserIDHeader, "user-allowed-cache")
	rr2 := httptest.NewRecorder()
	fixture.sidecar.ServeHTTP(rr2, req2)

	if rr2.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", rr2.Result().StatusCode)
	}
	if fixture.limiterHandler.callCount != 2 {
		t.Fatalf("expected limiter called again (2), got %d (allowed responses must not be cached)", fixture.limiterHandler.callCount)
	}
}

// Test Suite 18: Cache key isolation
func TestSidecar_CacheIsolation(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 100*time.Millisecond, true) // useHierarchical = true
	defer cleanup()

	// Verify tenant/user string concatenated collision protection due to pipe separator
	// Case 1: tenant="ab", user="c", path="/v1"
	// Case 2: tenant="a", user="bc", path="/v1"
	reqA := httptest.NewRequest(http.MethodGet, "/v1", nil)
	reqA.Header.Set("X-Tenant-ID", "ab")
	keyA := fixture.sidecar.cacheKey(reqA, "c") // "ab|c|/v1"

	reqB := httptest.NewRequest(http.MethodGet, "/v1", nil)
	reqB.Header.Set("X-Tenant-ID", "a")
	keyB := fixture.sidecar.cacheKey(reqB, "bc") // "a|bc|/v1"

	if keyA == keyB {
		t.Fatalf("cache key collision: keyA %q equals keyB %q", keyA, keyB)
	}
}

// Test Suite 9: Cache sweeper
func TestSidecar_CacheSweeper(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 50*time.Millisecond, false)
	defer cleanup()

	// Insert unexpired and expired items
	now := time.Now()
	fixture.sidecar.cache.Store("expired-key", CacheEntry{
		Allowed:   false,
		ExpiresAt: now.Add(-10 * time.Millisecond),
	})
	fixture.sidecar.cache.Store("unexpired-key", CacheEntry{
		Allowed:   false,
		ExpiresAt: now.Add(10 * time.Second),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start sweeper with 5ms interval
	fixture.sidecar.StartCacheSweeper(ctx, 5*time.Millisecond)

	// Wait for a sweep cycle to occur
	time.Sleep(20 * time.Millisecond)

	// Check if expired key is physically removed
	if _, ok := fixture.sidecar.cache.Load("expired-key"); ok {
		t.Error("expired key was not physically removed by cache sweeper")
	}

	// Check if unexpired key is still present
	if _, ok := fixture.sidecar.cache.Load("unexpired-key"); !ok {
		t.Error("unexpired key was incorrectly removed by cache sweeper")
	}

	// Cancel context to stop sweeper cleanly
	cancel()
	time.Sleep(10 * time.Millisecond)
}

func TestSidecar_AllowsNeverStoredInCache(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 50*time.Millisecond, false)
	defer cleanup()

	fixture.limiterHandler.allowed = true
	fixture.limiterHandler.limit = 10
	fixture.limiterHandler.remaining = 9

	// Send 50 allowed requests for distinct users
	for i := 0; i < 50; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
		req.Header.Set(identity.UserIDHeader, fmt.Sprintf("user-allowed-%d", i))
		rr := httptest.NewRecorder()
		fixture.sidecar.ServeHTTP(rr, req)
		if rr.Result().StatusCode != http.StatusOK {
			t.Fatalf("expected HTTP 200, got %d", rr.Result().StatusCode)
		}
	}

	// Verify that 0 allowed entries entered the cache map (H-02)
	cacheSize := 0
	fixture.sidecar.cache.Range(func(key, value interface{}) bool {
		cacheSize++
		return true
	})
	if cacheSize != 0 {
		t.Fatalf("expected 0 entries in cache for allowed requests, got %d", cacheSize)
	}
}

func TestSidecar_DenialCacheInsertCap(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, time.Hour, false)
	defer cleanup()

	fixture.sidecar.maxCacheSize = 8
	fixture.limiterHandler.allowed = false
	fixture.limiterHandler.limit = 5
	fixture.limiterHandler.remaining = 0

	for i := 0; i < 20; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
		req.Header.Set(identity.UserIDHeader, fmt.Sprintf("user-cap-%d", i))
		rr := httptest.NewRecorder()
		fixture.sidecar.ServeHTTP(rr, req)
		if rr.Result().StatusCode != http.StatusTooManyRequests {
			t.Fatalf("expected HTTP 429, got %d", rr.Result().StatusCode)
		}
	}

	cacheSize := 0
	fixture.sidecar.cache.Range(func(key, value interface{}) bool {
		cacheSize++
		return true
	})
	if cacheSize > 8 {
		t.Fatalf("expected denial cache cap of 8, got %d entries", cacheSize)
	}
}
