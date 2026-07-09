package main

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/identity"
)

// Test Suite 10: Singleflight collapse
func TestSidecar_SingleflightCollapse(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 100*time.Millisecond, false)
	defer cleanup()

	const numRequests = 100
	barrier := make(chan struct{})
	blockChan := make(chan struct{})
	fixture.limiterHandler.blockChan = blockChan

	var (
		allowed    atomic.Int64
		denied     atomic.Int64
		errs       atomic.Int64
		wg         sync.WaitGroup
	)

	// Make concurrent requests
	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier

			req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
			req.Header.Set(identity.UserIDHeader, "user-singleflight")
			rr := httptest.NewRecorder()
			fixture.sidecar.ServeHTTP(rr, req)

			resp := rr.Result()
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				allowed.Add(1)
			} else if resp.StatusCode == http.StatusTooManyRequests {
				denied.Add(1)
			} else {
				errs.Add(1)
			}
		}()
	}

	// Release the clients
	close(barrier)
	time.Sleep(10 * time.Millisecond)

	// Release the limiter block
	close(blockChan)
	wg.Wait()

	gotAllowed := allowed.Load()
	gotDenied := denied.Load()
	gotErrs := errs.Load()

	if gotErrs != 0 {
		t.Errorf("expected 0 errors, got %d", gotErrs)
	}
	if gotAllowed+gotDenied != int64(numRequests) {
		t.Errorf("expected allowed + denied == total (%d), got %d + %d = %d",
			numRequests, gotAllowed, gotDenied, gotAllowed+gotDenied)
	}

	// Singleflight collapse guarantee: exactly 1 limiter call should be performed
	if fixture.limiterHandler.callCount != 1 {
		t.Errorf("expected exactly 1 rate limiter call, got %d", fixture.limiterHandler.callCount)
	}
}

// Test Suite 11: Concurrent denial-cache miss
func TestSidecar_ConcurrentDenialCacheMiss(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 100*time.Millisecond, false)
	defer cleanup()

	// Configure limiter to deny
	fixture.limiterHandler.allowed = false
	fixture.limiterHandler.limit = 5
	fixture.limiterHandler.remaining = 0

	// Set up blocking channel
	barrier := make(chan struct{})
	blockChan := make(chan struct{})
	fixture.limiterHandler.blockChan = blockChan

	const numRequests = 50
	var (
		allowed    atomic.Int64
		denied     atomic.Int64
		errs       atomic.Int64
		wg         sync.WaitGroup
	)

	var upstreamCalled int64
	fixture.upstreamSrv.Close()
	fixture.upstreamSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&upstreamCalled, 1)
	}))
	fixture.sidecar.upstreamURL = fixture.upstreamSrv.URL

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier

			req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
			req.Header.Set(identity.UserIDHeader, "user-denied-sf")
			rr := httptest.NewRecorder()
			fixture.sidecar.ServeHTTP(rr, req)

			resp := rr.Result()
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				allowed.Add(1)
			} else if resp.StatusCode == http.StatusTooManyRequests {
				denied.Add(1)
			} else {
				errs.Add(1)
			}
		}()
	}

	close(barrier)
	time.Sleep(10 * time.Millisecond)

	close(blockChan)
	wg.Wait()

	gotAllowed := allowed.Load()
	gotDenied := denied.Load()
	gotErrs := errs.Load()

	if gotErrs != 0 {
		t.Errorf("expected 0 errors, got %d", gotErrs)
	}
	if gotAllowed != 0 {
		t.Errorf("expected 0 allowed, got %d", gotAllowed)
	}
	if gotDenied != int64(numRequests) {
		t.Errorf("expected %d denied requests, got %d", numRequests, gotDenied)
	}

	// Verify collapse
	if fixture.limiterHandler.callCount != 1 {
		t.Errorf("expected exactly 1 rate limiter call, got %d", fixture.limiterHandler.callCount)
	}
	if upstreamCalled != 0 {
		t.Errorf("upstream should not be called, got count %d", upstreamCalled)
	}
}

// Test Suite 10b: Singleflight key isolation
func TestSidecar_SingleflightKeyIsolation(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 100*time.Millisecond, false)
	defer cleanup()

	const numPerKey = 100
	barrier := make(chan struct{})
	blockChan := make(chan struct{})
	fixture.limiterHandler.blockChan = blockChan

	var (
		allowed atomic.Int64
		denied  atomic.Int64
		errs    atomic.Int64
		wg      sync.WaitGroup
	)

	// Make 100 concurrent requests for Key A ("user-A")
	for i := 0; i < numPerKey; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier

			req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
			req.Header.Set(identity.UserIDHeader, "user-A")
			rr := httptest.NewRecorder()
			fixture.sidecar.ServeHTTP(rr, req)

			resp := rr.Result()
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				allowed.Add(1)
			} else if resp.StatusCode == http.StatusTooManyRequests {
				denied.Add(1)
			} else {
				errs.Add(1)
			}
		}()
	}

	// Make 100 concurrent requests for Key B ("user-B")
	for i := 0; i < numPerKey; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier

			req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
			req.Header.Set(identity.UserIDHeader, "user-B")
			rr := httptest.NewRecorder()
			fixture.sidecar.ServeHTTP(rr, req)

			resp := rr.Result()
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				allowed.Add(1)
			} else if resp.StatusCode == http.StatusTooManyRequests {
				denied.Add(1)
			} else {
				errs.Add(1)
			}
		}()
	}

	// Release the clients simultaneously
	close(barrier)
	time.Sleep(10 * time.Millisecond)

	// Release the limiter block
	close(blockChan)
	wg.Wait()

	gotAllowed := allowed.Load()
	gotDenied := denied.Load()
	gotErrs := errs.Load()

	if gotErrs != 0 {
		t.Errorf("expected 0 errors, got %d", gotErrs)
	}
	if gotAllowed+gotDenied != 2*numPerKey {
		t.Errorf("expected all 200 requests complete, got %d", gotAllowed+gotDenied)
	}
	if gotAllowed != 2*numPerKey {
		t.Errorf("expected 200 allowed requests, got %d", gotAllowed)
	}

	// Assert exactly 2 limiter calls (1 for user-A, 1 for user-B)
	if fixture.limiterHandler.callCount != 2 {
		t.Errorf("expected exactly 2 rate limiter calls (1 per key), got %d", fixture.limiterHandler.callCount)
	}
}

