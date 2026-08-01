package main

import (
	"context"
	"time"
)

// RateLimiter is the shared contract for every algorithm implementation.
// Returning err distinguishes "quota exhausted" (429) from "backend broken" (503).
type RateLimiter interface {
	Allow(ctx context.Context, userID string) (allowed bool, remaining int, err error)
}

// RetryAfterLimiter is an optional capability: algorithms that can measure when
// quota actually frees up implement it so denials carry a precise Retry-After.
// Implementing it is not required — callers fall back to a config-derived
// estimate, so adding an algorithm never forces a change here.
type RetryAfterLimiter interface {
	AllowWithRetryAfter(ctx context.Context, userID string) (allowed bool, remaining int, retryAfter time.Duration, err error)
}

// allowWithRetryAfter runs the limiter, using the precise retry hint when the
// implementation offers one. A zero duration means "no hint available".
func allowWithRetryAfter(ctx context.Context, l RateLimiter, userID string) (bool, int, time.Duration, error) {
	if ra, ok := l.(RetryAfterLimiter); ok {
		return ra.AllowWithRetryAfter(ctx, userID)
	}
	allowed, remaining, err := l.Allow(ctx, userID)
	return allowed, remaining, 0, err
}
