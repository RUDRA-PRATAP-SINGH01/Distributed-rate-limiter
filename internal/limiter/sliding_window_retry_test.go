package limiter

// sliding_window_retry_test.go
// Covers the retry hint returned by sliding_window.lua on denial.
//
// A sliding log has no shared reset instant, so the wait is the time until the
// OLDEST in-window entry ages out — never the full window. These tests pin that
// contract so the header cannot silently regress to fixed-window semantics.

import (
	"context"
	"testing"
	"time"
)

func TestSlidingWindow_RetryAfter_ShorterThanFullWindow(t *testing.T) {
	_, rdb := newMR(t)
	window := time.Second
	sw := NewRedisSlidingWindow(rdb, 2, window)
	uid := "sw-retry-hint"

	for i := 0; i < 2; i++ {
		if ok, _ := allow1SW(t, sw, uid); !ok {
			t.Fatalf("request %d must be allowed within capacity", i+1)
		}
	}

	// Let the oldest entry age partway through the window so a full-window
	// answer would be visibly wrong.
	time.Sleep(300 * time.Millisecond)

	allowed, _, retryAfter, err := sw.AllowWithRetryAfter(context.Background(), uid)
	if err != nil {
		t.Fatalf("AllowWithRetryAfter: %v", err)
	}
	if allowed {
		t.Fatal("third request must be denied at limit 2")
	}
	if retryAfter <= 0 {
		t.Fatal("denial must carry a positive retry hint")
	}
	if retryAfter > window {
		t.Fatalf("retry hint %v exceeds the window %v", retryAfter, window)
	}
	// The oldest entry already burned ~300ms, so a fixed-window answer (the
	// full second) would over-state the wait.
	if retryAfter > 800*time.Millisecond {
		t.Fatalf("retry hint %v looks like a full-window answer, want the age-out of the oldest entry", retryAfter)
	}
}

func TestSlidingWindow_RetryAfter_AllowedRequestHasNoHint(t *testing.T) {
	_, rdb := newMR(t)
	sw := NewRedisSlidingWindow(rdb, 3, time.Second)

	allowed, _, retryAfter, err := sw.AllowWithRetryAfter(context.Background(), "sw-retry-allowed")
	if err != nil {
		t.Fatalf("AllowWithRetryAfter: %v", err)
	}
	if !allowed {
		t.Fatal("first request must be allowed")
	}
	if retryAfter != 0 {
		t.Fatalf("allowed request must not carry a retry hint, got %v", retryAfter)
	}
}

// Waiting exactly the advertised duration must admit the caller: the trim is
// inclusive of windowStart, so the oldest entry is gone by then. If this fails,
// clients that honor Retry-After get denied twice and back off further.
func TestSlidingWindow_RetryAfter_WaitingThatLongSucceeds(t *testing.T) {
	_, rdb := newMR(t)
	sw := NewRedisSlidingWindow(rdb, 1, 300*time.Millisecond)
	uid := "sw-retry-boundary"

	if ok, _ := allow1SW(t, sw, uid); !ok {
		t.Fatal("first request must be allowed")
	}

	allowed, _, retryAfter, err := sw.AllowWithRetryAfter(context.Background(), uid)
	if err != nil {
		t.Fatalf("AllowWithRetryAfter: %v", err)
	}
	if allowed {
		t.Fatal("second request must be denied at limit 1")
	}

	time.Sleep(retryAfter)

	allowed, _, _, err = sw.AllowWithRetryAfter(context.Background(), uid)
	if err != nil {
		t.Fatalf("AllowWithRetryAfter after wait: %v", err)
	}
	if !allowed {
		t.Fatalf("waiting the advertised %v must free a slot", retryAfter)
	}
}

// A misconfigured limit denies with an empty ZSET, so there is no oldest entry
// to measure. The script must return a zero hint instead of erroring.
func TestSlidingWindow_RetryAfter_NonPositiveLimitDegradesSafely(t *testing.T) {
	_, rdb := newMR(t)
	sw := NewRedisSlidingWindow(rdb, 0, time.Second)

	allowed, remaining, retryAfter, err := sw.AllowWithRetryAfter(context.Background(), "sw-retry-zero-limit")
	if err != nil {
		t.Fatalf("limit 0 must not error, got %v", err)
	}
	if allowed {
		t.Fatal("limit 0 must deny every request")
	}
	if remaining != 0 {
		t.Fatalf("remaining: got %d, want 0", remaining)
	}
	if retryAfter != 0 {
		t.Fatalf("no oldest entry means no hint, got %v", retryAfter)
	}
}
