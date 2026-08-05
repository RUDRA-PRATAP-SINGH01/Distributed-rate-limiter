package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/idempotency"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/identity"
)

func TestSidecar_Upstream5xxDoesNotPoisonIdempotencyKey(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 30*time.Millisecond, false)
	defer cleanup()

	cfg := idempotency.DefaultConfig()
	cfg.LockTTL = 150
	cfg.CompletedTTL = 60_000
	store := idempotency.NewRedisStore(fixture.rdb, cfg)
	fixture.sidecar.SetIdempotency(store, cfg)

	fixture.upstreamHandler.mu.Lock()
	fixture.upstreamHandler.statusCode = http.StatusServiceUnavailable
	fixture.upstreamHandler.body = "upstream-down"
	fixture.upstreamHandler.mu.Unlock()

	post := func() *http.Response {
		req := httptest.NewRequest(http.MethodPost, "/api/orders", strings.NewReader(`{"n":1}`))
		req.Header.Set(identity.UserIDHeader, "user-idem-5xx")
		req.Header.Set("Idempotency-Key", "k-n04")
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		fixture.sidecar.ServeHTTP(rr, req)
		return rr.Result()
	}

	resp := post()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("first call: expected 503, got %d", resp.StatusCode)
	}

	time.Sleep(250 * time.Millisecond)

	fixture.upstreamHandler.mu.Lock()
	fixture.upstreamHandler.statusCode = http.StatusOK
	fixture.upstreamHandler.body = "recovered"
	fixture.upstreamHandler.mu.Unlock()

	resp2 := post()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("after Fail LockTTL, retry must hit recovered upstream, got %d", resp2.StatusCode)
	}
}

func TestSidecar_QueryTenantIgnoredWhenDisallowed(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 30*time.Millisecond, true)
	defer cleanup()
	fixture.sidecar.allowQueryUserID = false

	req := httptest.NewRequest(http.MethodGet, "/api/data?tenant_id=spoofed-tenant", nil)
	req.Header.Set(identity.UserIDHeader, "user-n07")
	if got := fixture.sidecar.cacheKey(req, "user-n07"); !strings.HasPrefix(got, identity.DefaultTenant+"|") {
		t.Fatalf("query tenant must not enter cache key, got %q", got)
	}

	bad := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	bad.Header.Set(identity.UserIDHeader, "user-n07")
	bad.Header.Set(identity.TenantIDHeader, "no spaces allowed")
	rr := httptest.NewRecorder()
	fixture.sidecar.ServeHTTP(rr, bad)
	if rr.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid tenant header must 400, got %d", rr.Result().StatusCode)
	}
}
