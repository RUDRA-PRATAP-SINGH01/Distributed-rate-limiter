package main

import "context"

// RateLimiter is the shared contract for every algorithm implementation.
// Returning err distinguishes "quota exhausted" (429) from "backend broken" (503).
type RateLimiter interface {
	Allow(ctx context.Context, userID string) (allowed bool, remaining int, err error)
}
