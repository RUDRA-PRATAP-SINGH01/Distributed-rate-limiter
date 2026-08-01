package main

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

// The sliding-window 429 must advertise the wait until the oldest entry ages
// out, not the whole window. Handing every denied client the same full-window
// value under-uses the quota and synchronizes their retries.
func TestCheckHandler_SlidingWindow_RetryAfterTracksOldestEntry(t *testing.T) {
	fixture, cleanup := newTestFixture(t, func(c *Config) {
		c.Algorithm = "sliding"
		c.Capacity = 1
		c.WindowSec = 2
	})
	defer cleanup()

	check := func() *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, fixture.mainURL+"/check", nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		req.Header.Set("X-API-Key", fixture.cfg.InternalAPIKey)
		req.Header.Set("X-User-ID", "carol-sliding")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to perform request: %v", err)
		}
		return resp
	}

	resp := check()
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", resp.StatusCode)
	}

	// Burn most of the window so the remaining wait is clearly under WindowSec.
	time.Sleep(1100 * time.Millisecond)

	resp = check()
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second request: expected 429, got %d", resp.StatusCode)
	}

	header := resp.Header.Get("Retry-After")
	if header == "" {
		t.Fatal("429 must carry a Retry-After header")
	}
	secs, err := strconv.Atoi(header)
	if err != nil {
		t.Fatalf("Retry-After must be whole seconds per RFC 9110, got %q", header)
	}
	if secs < 1 {
		t.Fatalf("Retry-After must be at least 1 to avoid an immediate retry, got %d", secs)
	}
	if secs >= fixture.cfg.WindowSec {
		t.Fatalf("Retry-After %d is the full window %d — fixed-window semantics regressed",
			secs, fixture.cfg.WindowSec)
	}
}

func TestRetryAfterHeader_FallsBackWhenLimiterGivesNoHint(t *testing.T) {
	cfg := Config{Algorithm: "sliding", WindowSec: 45}
	if got := retryAfterHeader(cfg, 0); got != "45" {
		t.Fatalf("no hint must fall back to the config estimate: got %q, want \"45\"", got)
	}

	// Sub-second waits round up so clients never retry instantly.
	if got := retryAfterHeader(cfg, 200*time.Millisecond); got != "1" {
		t.Fatalf("sub-second hint must round up to 1, got %q", got)
	}

	if got := retryAfterHeader(cfg, 2500*time.Millisecond); got != "3" {
		t.Fatalf("measured hint must round up to whole seconds: got %q, want \"3\"", got)
	}
}
