package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/circuitbreaker"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/identity"
)

// Test Suite 12: Circuit breaker transitions
func TestSidecar_CircuitBreaker(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 100*time.Millisecond, false) // failOpen = false
	defer cleanup()

	ctx := context.Background()

	// Verify initial Closed state
	state, err := fixture.sidecar.limiterCircuit.GetState(ctx, circuitbreaker.TargetCentralLimiter)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.State != circuitbreaker.StateClosed {
		t.Fatalf("expected closed breaker, got %s", state.State)
	}

	// 1. Healthy pass-through does not trip the breaker
	req1 := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req1.Header.Set(identity.UserIDHeader, "user-cb-healthy")
	rr1 := httptest.NewRecorder()
	fixture.sidecar.ServeHTTP(rr1, req1)

	if rr1.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", rr1.Result().StatusCode)
	}

	// 2. Tripping the breaker with failures (e.g. 500 Errors from central limiter)
	fixture.limiterHandler.statusCode = http.StatusInternalServerError

	// Trigger 2 failures (threshold = 2 consecutive)
	for i := 0; i < 2; i++ {
		reqFail := httptest.NewRequest(http.MethodGet, "/api/data", nil)
		reqFail.Header.Set(identity.UserIDHeader, "user-cb-fail")
		rrFail := httptest.NewRecorder()
		fixture.sidecar.ServeHTTP(rrFail, reqFail)

		if rrFail.Result().StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("expected HTTP 503, got %d", rrFail.Result().StatusCode)
		}
	}

	// Verify breaker is now Open
	state2, _ := fixture.sidecar.limiterCircuit.GetState(ctx, circuitbreaker.TargetCentralLimiter)
	if state2.State != circuitbreaker.StateOpen {
		t.Fatalf("expected breaker to be open, got %s", state2.State)
	}

	// While Open: limiter is NOT called anymore
	fixture.limiterHandler.callCount = 0
	reqOpen := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	reqOpen.Header.Set(identity.UserIDHeader, "user-cb-open")
	rrOpen := httptest.NewRecorder()
	fixture.sidecar.ServeHTTP(rrOpen, reqOpen)

	if rrOpen.Result().StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected HTTP 503 on open breaker, got %d", rrOpen.Result().StatusCode)
	}
	if fixture.limiterHandler.callCount != 0 {
		t.Fatalf("limiter must not be called when breaker is open, callCount = %d", fixture.limiterHandler.callCount)
	}

	// 3. Cooldown and Probe recovery
	// Cooldown interval is 50ms, sleep 65ms
	time.Sleep(65 * time.Millisecond)

	// Recover limiter health
	fixture.limiterHandler.statusCode = http.StatusOK
	fixture.limiterHandler.allowed = true

	// First request in recovery should probe the limiter (Half-Open)
	reqProbe := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	reqProbe.Header.Set(identity.UserIDHeader, "user-cb-probe")
	rrProbe := httptest.NewRecorder()
	fixture.sidecar.ServeHTTP(rrProbe, reqProbe)

	if rrProbe.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200 on successful probe, got %d", rrProbe.Result().StatusCode)
	}

	// Trigger second probe success to satisfy HalfOpenSuccessRequired (default is 2)
	reqProbe2 := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	reqProbe2.Header.Set(identity.UserIDHeader, "user-cb-probe2")
	rrProbe2 := httptest.NewRecorder()
	fixture.sidecar.ServeHTTP(rrProbe2, reqProbe2)

	// Verify state transitioned back to Closed
	state3, _ := fixture.sidecar.limiterCircuit.GetState(ctx, circuitbreaker.TargetCentralLimiter)
	if state3.State != circuitbreaker.StateClosed {
		t.Fatalf("expected breaker to close after recovery, got %s", state3.State)
	}
}

// 4. Normal 429 Rate Limit Denials do not trip the breaker
func TestSidecar_CB_IgnoreRateLimitDenials(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 100*time.Millisecond, false)
	defer cleanup()

	ctx := context.Background()

	// Set limiter to deny with 429
	fixture.limiterHandler.allowed = false
	fixture.limiterHandler.statusCode = http.StatusTooManyRequests

	for i := 0; i < 5; i++ {
		req429 := httptest.NewRequest(http.MethodGet, "/api/data", nil)
		req429.Header.Set(identity.UserIDHeader, "user-cb-429")
		rr429 := httptest.NewRecorder()
		fixture.sidecar.ServeHTTP(rr429, req429)

		if rr429.Result().StatusCode != http.StatusTooManyRequests {
			t.Fatalf("expected HTTP 429, got %d", rr429.Result().StatusCode)
		}
	}

	// Verify breaker remains Closed
	state, _ := fixture.sidecar.limiterCircuit.GetState(ctx, circuitbreaker.TargetCentralLimiter)
	if state.State != circuitbreaker.StateClosed {
		t.Fatalf("expected breaker to remain closed on rate-limit denials, got %s", state.State)
	}
}

// 5. Malformed JSON on 200 OK must set callErr and trip the circuit breaker (H-11)
func TestSidecar_CB_JSONDecodeErrorTripsCircuit(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 100*time.Millisecond, false)
	defer cleanup()

	ctx := context.Background()

	// Return 200 OK with malformed JSON body
	fixture.limiterHandler.statusCode = http.StatusOK
	fixture.limiterHandler.bodyJSON = "{malformed-json-payload"

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
		req.Header.Set(identity.UserIDHeader, "user-cb-decode-err")
		rr := httptest.NewRecorder()
		fixture.sidecar.ServeHTTP(rr, req)

		if rr.Result().StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("iter %d: expected HTTP 503 on decode failure, got %d", i, rr.Result().StatusCode)
		}
	}

	// Verify breaker tripped to StateOpen (H-11)
	state, err := fixture.sidecar.limiterCircuit.GetState(ctx, circuitbreaker.TargetCentralLimiter)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.State != circuitbreaker.StateOpen {
		t.Fatalf("expected breaker to trip to open on decode failures, got %s", state.State)
	}
}

// Local Allow rejects must not Record OutcomeSuccess (N-03). Otherwise half-open
// excess probes can close the circuit without a real limiter call succeeding.
func TestSidecar_CB_LocalRejectDoesNotRecordSuccess(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 100*time.Millisecond, false)
	defer cleanup()

	cbCfg := circuitbreaker.DefaultConfig()
	cbCfg.OpenCooldownMs = 50
	cbCfg.MinSamples = 2
	cbCfg.ConsecutiveFailures = 2
	cbCfg.HalfOpenMaxProbes = 1
	cbCfg.HalfOpenSuccessRequired = 2
	store := circuitbreaker.NewRedisStore(fixture.rdb, cbCfg)
	fixture.sidecar.SetLimiterCircuit(circuitbreaker.NewBreaker(store))

	ctx := context.Background()
	fixture.limiterHandler.statusCode = http.StatusInternalServerError
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
		req.Header.Set(identity.UserIDHeader, "user-cb-n03")
		rr := httptest.NewRecorder()
		fixture.sidecar.ServeHTTP(rr, req)
	}
	state, err := fixture.sidecar.limiterCircuit.GetState(ctx, circuitbreaker.TargetCentralLimiter)
	if err != nil {
		t.Fatal(err)
	}
	if state.State != circuitbreaker.StateOpen {
		t.Fatalf("expected open, got %s", state.State)
	}

	time.Sleep(65 * time.Millisecond)

	block := make(chan struct{})
	fixture.limiterHandler.mu.Lock()
	fixture.limiterHandler.callCount = 0
	fixture.limiterHandler.blockChan = block
	fixture.limiterHandler.statusCode = http.StatusOK
	fixture.limiterHandler.allowed = true
	fixture.limiterHandler.mu.Unlock()

	done := make(chan int, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
		req.Header.Set(identity.UserIDHeader, "user-cb-n03-probe")
		rr := httptest.NewRecorder()
		fixture.sidecar.ServeHTTP(rr, req)
		done <- rr.Result().StatusCode
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		fixture.limiterHandler.mu.Lock()
		n := fixture.limiterHandler.callCount
		fixture.limiterHandler.mu.Unlock()
		if n >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("probe never reached limiter")
		}
		time.Sleep(5 * time.Millisecond)
	}

	for i := 0; i < 4; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
		req.Header.Set(identity.UserIDHeader, "user-cb-n03-excess")
		rr := httptest.NewRecorder()
		fixture.sidecar.ServeHTTP(rr, req)
		if rr.Result().StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("excess %d: expected 503, got %d", i, rr.Result().StatusCode)
		}
	}

	state2, err := fixture.sidecar.limiterCircuit.GetState(ctx, circuitbreaker.TargetCentralLimiter)
	if err != nil {
		t.Fatal(err)
	}
	if state2.State != circuitbreaker.StateHalfOpen {
		t.Fatalf("excess rejects must not close the circuit, got %s", state2.State)
	}

	close(block)
	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Fatalf("probe status %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked probe hung")
	}
}
