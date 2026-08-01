package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/circuitbreaker"
)

func TestRedisFailure_Handling(t *testing.T) {
	fixture, cleanup := newTestFixture(t, nil)
	defer cleanup()

	// Force Redis unreachable by closing the client connection
	fixture.rdb.Close()

	// 1. Verify /check endpoint handles failure gracefully without panic
	reqCheck, err := http.NewRequest(http.MethodGet, fixture.mainURL+"/check", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	reqCheck.Header.Set("X-API-Key", fixture.cfg.InternalAPIKey)
	reqCheck.Header.Set("X-User-ID", "alice-fail")

	respCheck, err := http.DefaultClient.Do(reqCheck)
	if err != nil {
		t.Fatalf("failed to perform request: %v", err)
	}
	defer respCheck.Body.Close()

	if respCheck.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected HTTP 503 on Redis failure, got %d", respCheck.StatusCode)
	}

	bodyBytes, err := io.ReadAll(respCheck.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	bodyStr := string(bodyBytes)

	if strings.Contains(bodyStr, fixture.cfg.RedisAddr) {
		t.Errorf("Redis address leaked in error body: %s", bodyStr)
	}
	if strings.Contains(bodyStr, "redis") && !strings.Contains(bodyStr, "Rate limiter unavailable") {
		t.Errorf("Potential secret/implementation leak: %s", bodyStr)
	}

	// 2. Verify /check_hierarchical endpoint handles Redis failure
	reqHier, err := http.NewRequest(http.MethodGet, fixture.mainURL+"/check_hierarchical", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	reqHier.Header.Set("X-API-Key", fixture.cfg.InternalAPIKey)
	reqHier.Header.Set("X-User-ID", "alice-fail")

	respHier, err := http.DefaultClient.Do(reqHier)
	if err != nil {
		t.Fatalf("failed to perform request: %v", err)
	}
	defer respHier.Body.Close()

	if respHier.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected HTTP 503 on hierarchical Redis failure, got %d", respHier.StatusCode)
	}
}

// TestRedisFailure_CircuitBreakerTrips proves the Redis-dependency breaker
// (LocalStore) opens after consecutive Redis failures and then fail-fasts
// with circuit_state=open without needing Redis for Allow().
func TestRedisFailure_CircuitBreakerTrips(t *testing.T) {
	fixture, cleanup := newTestFixture(t, func(c *Config) {
		c.Algorithm = "token"
	})
	defer cleanup()

	fixture.rdb.Close()

	// Fixture CB: ConsecutiveFailures=2, MinSamples=2. Drive enough failures to trip.
	for i := 0; i < 3; i++ {
		req, err := http.NewRequest(http.MethodGet, fixture.mainURL+"/check", nil)
		if err != nil {
			t.Fatalf("create request: %v", err)
		}
		req.Header.Set("X-API-Key", fixture.cfg.InternalAPIKey)
		req.Header.Set("X-User-ID", "user-cb-trip")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("request %d: expected 503 while Redis down, got %d", i, resp.StatusCode)
		}
	}

	snap, err := redisCircuit.GetState(t.Context(), circuitbreaker.TargetRedis)
	if err != nil {
		t.Fatalf("GetState must succeed on LocalStore even when Redis is closed: %v", err)
	}
	if snap.State != circuitbreaker.StateOpen {
		t.Fatalf("expected circuit open after consecutive Redis failures, got %s (snap=%+v)", snap.State, snap)
	}

	// Next check must reject from the local open circuit (fail-fast), not from a Redis RTT.
	start := time.Now()
	req, err := http.NewRequest(http.MethodGet, fixture.mainURL+"/check", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("X-API-Key", fixture.cfg.InternalAPIKey)
	req.Header.Set("X-User-ID", "user-cb-trip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post-trip request: %v", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 after trip, got %d", resp.StatusCode)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["circuit_state"] != string(circuitbreaker.StateOpen) {
		t.Fatalf("expected circuit_state=open after trip, got body=%v", body)
	}
	// Fail-fast: local Allow must not wait on Redis dial/read timeouts (~500ms+).
	if elapsed > 200*time.Millisecond {
		t.Fatalf("open circuit should fail-fast; took %s (likely still hitting Redis)", elapsed)
	}
}
