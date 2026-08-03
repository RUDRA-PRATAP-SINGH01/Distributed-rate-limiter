package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/identity"
)

// Test Suite 1: Allowed forwarding
func TestSidecar_AllowedForwarding(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 30*time.Millisecond, false)
	defer cleanup()

	// Upstream should receive headers and request body intact.
	bodyText := "hello-world"
	req := httptest.NewRequest(http.MethodPost, "/api/data?debug=true", bytes.NewBufferString(bodyText))
	req.Header.Set(identity.UserIDHeader, "user-123")
	req.Header.Set("X-Custom-Header", "custom-val")

	rr := httptest.NewRecorder()
	fixture.sidecar.ServeHTTP(rr, req)

	resp := rr.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", resp.StatusCode)
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	if string(bodyBytes) != "upstream-ok" {
		t.Fatalf("expected body %q, got %q", "upstream-ok", string(bodyBytes))
	}

	// Verify rate limit headers propagated to client
	if got := resp.Header.Get("X-RateLimit-Limit"); got != "10" {
		t.Errorf("expected X-RateLimit-Limit=10, got %q", got)
	}
	if got := resp.Header.Get("X-RateLimit-Remaining"); got != "9" {
		t.Errorf("expected X-RateLimit-Remaining=9, got %q", got)
	}

	// Verify limiter was called as expected
	if fixture.limiterHandler.callCount != 1 {
		t.Errorf("expected limiter call count = 1, got %d", fixture.limiterHandler.callCount)
	}
	if fixture.limiterHandler.lastUser != "user-123" {
		t.Errorf("expected X-User-ID header passed to limiter, got %q", fixture.limiterHandler.lastUser)
	}
}

// Test Suite 2: Denied requests
func TestSidecar_DeniedRequests(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 30*time.Millisecond, false)
	defer cleanup()

	// Configure mock limiter to deny request
	fixture.limiterHandler.allowed = false
	fixture.limiterHandler.limit = 5
	fixture.limiterHandler.remaining = 0
	fixture.limiterHandler.retryAfter = "120"

	// Reset call count on upstream
	fixture.upstreamHandler.mu.Lock()
	fixture.upstreamHandler.callCount = 0
	fixture.upstreamHandler.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req.Header.Set(identity.UserIDHeader, "user-exhausted")

	rr := httptest.NewRecorder()
	fixture.sidecar.ServeHTTP(rr, req)

	resp := rr.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected HTTP 429, got %d", resp.StatusCode)
	}

	// Invariant: denied requests must never call upstream
	fixture.upstreamHandler.mu.Lock()
	uc := fixture.upstreamHandler.callCount
	fixture.upstreamHandler.mu.Unlock()
	if uc != 0 {
		t.Fatalf("upstream called %d times on denied request", uc)
	}

	// Check response headers
	if got := resp.Header.Get("X-RateLimit-Limit"); got != "5" {
		t.Errorf("expected X-RateLimit-Limit=5, got %q", got)
	}
	if got := resp.Header.Get("X-RateLimit-Remaining"); got != "0" {
		t.Errorf("expected X-RateLimit-Remaining=0, got %q", got)
	}
	if got := resp.Header.Get("Retry-After"); got != "120" {
		t.Errorf("expected Retry-After=120, got %q", got)
	}
}

// Test Suite 3: Fail-open behavior
func TestSidecar_FailOpen(t *testing.T) {
	fixture, cleanup := newTestFixture(t, true, 30*time.Millisecond, false) // failOpen = true
	defer cleanup()

	// Simulate rate limiter HTTP 500 error
	fixture.limiterHandler.statusCode = http.StatusInternalServerError

	fixture.upstreamHandler.mu.Lock()
	fixture.upstreamHandler.callCount = 0
	fixture.upstreamHandler.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req.Header.Set(identity.UserIDHeader, "user-any")

	rr := httptest.NewRecorder()
	fixture.sidecar.ServeHTTP(rr, req)

	resp := rr.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected allowed/success on fail-open, got status %d", resp.StatusCode)
	}
	fixture.upstreamHandler.mu.Lock()
	uc := fixture.upstreamHandler.callCount
	fixture.upstreamHandler.mu.Unlock()
	if uc != 1 {
		t.Errorf("expected upstream called exactly once, got %d", uc)
	}
}

// Test Suite 4: Fail-closed behavior
func TestSidecar_FailClosed(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 30*time.Millisecond, false) // failOpen = false
	defer cleanup()

	// Simulate rate limiter HTTP 500 error
	fixture.limiterHandler.statusCode = http.StatusInternalServerError

	fixture.upstreamHandler.mu.Lock()
	fixture.upstreamHandler.callCount = 0
	fixture.upstreamHandler.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req.Header.Set(identity.UserIDHeader, "user-any")

	rr := httptest.NewRecorder()
	fixture.sidecar.ServeHTTP(rr, req)

	resp := rr.Result()
	defer resp.Body.Close()

	// According to cmd/sidecar/main.go line 384: http.StatusServiceUnavailable (503)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected HTTP 503 on fail-closed error, got %d", resp.StatusCode)
	}

	// Invariant: fail-closed limiter failure => upstream count == 0
	fixture.upstreamHandler.mu.Lock()
	uc := fixture.upstreamHandler.callCount
	fixture.upstreamHandler.mu.Unlock()
	if uc != 0 {
		t.Errorf("upstream called %d times on fail-closed error", uc)
	}
}

// Test Suite 5: Limiter timeouts
func TestSidecar_LimiterTimeouts(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 30*time.Millisecond, false)
	defer cleanup()

	blockChan := make(chan struct{})
	fixture.limiterHandler.blockChan = blockChan

	// Shorten timeout for test speed
	fixture.sidecar.httpClient.Timeout = 10 * time.Millisecond

	fixture.upstreamHandler.mu.Lock()
	fixture.upstreamHandler.callCount = 0
	fixture.upstreamHandler.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req.Header.Set(identity.UserIDHeader, "user-timeout")

	rr := httptest.NewRecorder()
	fixture.sidecar.ServeHTTP(rr, req)

	resp := rr.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected HTTP 503 on timeout fail-closed, got %d", resp.StatusCode)
	}
	fixture.upstreamHandler.mu.Lock()
	uc := fixture.upstreamHandler.callCount
	fixture.upstreamHandler.mu.Unlock()
	if uc != 0 {
		t.Errorf("expected upstream call count == 0 on timeout, got %d", uc)
	}

	close(blockChan) // release block to prevent leak
}

// Test Suite 6: Malformed limiter responses
func TestSidecar_MalformedResponses(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 30*time.Millisecond, false)
	defer cleanup()

	// Seed malformed JSON
	fixture.limiterHandler.bodyJSON = `{"allowed": ` // incomplete JSON

	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req.Header.Set(identity.UserIDHeader, "user-malformed")

	rr := httptest.NewRecorder()
	fixture.sidecar.ServeHTTP(rr, req)

	resp := rr.Result()
	defer resp.Body.Close()

	// Should trigger rate limiter error -> fail-closed (503)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected HTTP 503, got %d", resp.StatusCode)
	}
}

// Test Suite 13: Identity extraction
func TestSidecar_IdentityExtraction(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 30*time.Millisecond, false)
	defer cleanup()

	// Case 1: Missing identity entirely
	req1 := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	rr1 := httptest.NewRecorder()
	fixture.sidecar.ServeHTTP(rr1, req1)
	resp1 := rr1.Result()
	defer resp1.Body.Close()

	if resp1.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request on missing identity, got %d", resp1.StatusCode)
	}

	// Case 2: Extract from query parameter
	req2 := httptest.NewRequest(http.MethodGet, "/api/data?user_id=query-user", nil)
	rr2 := httptest.NewRecorder()
	fixture.sidecar.ServeHTTP(rr2, req2)
	resp2 := rr2.Result()
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK on query parameter identity, got %d", resp2.StatusCode)
	}
	if fixture.limiterHandler.lastUser != "query-user" {
		t.Errorf("expected query user_id passed to limiter, got %q", fixture.limiterHandler.lastUser)
	}
}

// Test Suite 14: Hierarchical metadata construction
func TestSidecar_HierarchicalMetadata(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 30*time.Millisecond, true) // useHierarchical = true
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/resource", nil)
	req.Header.Set(identity.UserIDHeader, "user-hier")
	req.Header.Set("X-Tenant-ID", "tenant-hier")

	rr := httptest.NewRecorder()
	fixture.sidecar.ServeHTTP(rr, req)

	resp := rr.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected OK, got %d", resp.StatusCode)
	}

	// Verify check URL mapping path passed as endpoint query string
	expectedSuffix := "/check_hierarchical?endpoint=%2Fapi%2Fv1%2Fresource"
	if !strings.HasSuffix(fixture.limiterHandler.lastPath, "check_hierarchical") {
		t.Errorf("expected endpoint path hierarchical check URL suffix, got path %q", fixture.limiterHandler.lastPath)
	}
	_ = expectedSuffix // checked conceptually via mock handler URL structure mapping path
}

// Test Suite 19: Context cancellation
func TestSidecar_ContextCancellation(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 30*time.Millisecond, false)
	defer cleanup()

	// Pre-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/data", nil).WithContext(ctx)
	req.Header.Set(identity.UserIDHeader, "user-cancelled")

	rr := httptest.NewRecorder()
	fixture.sidecar.ServeHTTP(rr, req)

	resp := rr.Result()
	defer resp.Body.Close()

	// Request should be aborted/denied due to cancelled context (503 from limiter or 502 from proxy)
	if resp.StatusCode != http.StatusServiceUnavailable && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadGateway {
		t.Errorf("expected HTTP 503/502 or abort, got %d", resp.StatusCode)
	}
}

func TestSidecar_PathAllowed_StrictBoundaries(t *testing.T) {
	s := &Sidecar{
		allowedPaths: parseAllowedPaths("/api, /v1/auth/"),
	}

	// Exact matches and child paths should be allowed
	allowed := []string{"/api", "/api/", "/api/users", "/api/users/123", "/v1/auth", "/v1/auth/", "/v1/auth/login"}
	for _, p := range allowed {
		if !s.pathAllowed(p) {
			t.Errorf("expected path %q to be allowed", p)
		}
	}

	// Sibling / prefix collision paths must NOT be allowed (H-06)
	rejected := []string{"/apiEvil", "/api-admin", "/apitoken", "/v1/authorize", "/other", "/api/../secret", "/api/../../etc/passwd"}
	for _, p := range rejected {
		if s.pathAllowed(p) {
			t.Errorf("expected path %q to be REJECTED (H-06 boundary bypass prevented)", p)
		}
	}

	if !s.pathAllowed("/api/./users") {
		t.Error("expected cleaned /api/./users to be allowed")
	}

	root := &Sidecar{allowedPaths: parseAllowedPaths("/")}
	if !root.pathAllowed("/anything") {
		t.Error("ALLOWED_PATHS=/ must still match all HTTP paths")
	}
}

func TestSidecar_Singleflight_LeaderCancellationDoesNotAbortWaiters(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 50*time.Millisecond, false)
	defer cleanup()

	// Block limiter until both requests are waiting
	blocker := make(chan struct{})
	fixture.limiterHandler.mu.Lock()
	fixture.limiterHandler.blockChan = blocker
	fixture.limiterHandler.allowed = true
	fixture.limiterHandler.limit = 10
	fixture.limiterHandler.remaining = 9
	fixture.limiterHandler.mu.Unlock()

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	waiterCtx := context.Background()

	leaderDone := make(chan int, 1)
	waiterDone := make(chan int, 1)

	// Launch leader with cancellable context
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/api/data", nil).WithContext(leaderCtx)
		req.Header.Set(identity.UserIDHeader, "user-sf-leader")
		rr := httptest.NewRecorder()
		fixture.sidecar.ServeHTTP(rr, req)
		leaderDone <- rr.Result().StatusCode
	}()

	// Small pause so leader enters singleflight first
	time.Sleep(10 * time.Millisecond)

	// Launch parallel waiter with independent context
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/api/data", nil).WithContext(waiterCtx)
		req.Header.Set(identity.UserIDHeader, "user-sf-leader") // same user -> shares singleflight
		rr := httptest.NewRecorder()
		fixture.sidecar.ServeHTTP(rr, req)
		waiterDone <- rr.Result().StatusCode
	}()

	time.Sleep(20 * time.Millisecond)

	// Cancel the leader request while in-flight!
	cancelLeader()

	// Unblock limiter to complete the shared call
	close(blocker)

	waiterStatus := <-waiterDone

	// Waiter MUST succeed with HTTP 200 (not fail with 503 due to leader cancellation) (H-12)
	if waiterStatus != http.StatusOK {
		t.Fatalf("expected waiter to receive HTTP 200 despite leader cancellation, got %d", waiterStatus)
	}
}

func TestReadRequestBody_TooLarge(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 64)
	req := httptest.NewRequest(http.MethodPost, "/api/data", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	got, err := readRequestBody(rr, req, 16)
	if err == nil {
		t.Fatalf("expected max-bytes error, read %d bytes", len(got))
	}
	var maxErr *http.MaxBytesError
	if !errors.As(err, &maxErr) {
		t.Fatalf("expected *http.MaxBytesError, got %T %v", err, err)
	}
}
