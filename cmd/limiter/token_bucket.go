package main

import (
	"sync"
	"time"
)

// In-memory token bucket used by unit tests and as a reference implementation.
// Production uses RedisAtomicTokenBucket — this version shows the refill math without network I/O.

type userBucket struct {
	tokens     float64
	lastRefill time.Time
}

type TokenBucket struct {
	capacity   int
	refillRate float64
	mu         sync.Mutex
	users      map[string]*userBucket // per-user isolation: one noisy neighbor cannot drain others
}

func NewTokenBucket(capacity int, refillRate float64) *TokenBucket {
	return &TokenBucket{
		capacity:   capacity,
		refillRate: refillRate,
		users:      make(map[string]*userBucket),
	}
}

func (tb *TokenBucket) bucketFor(userID string) *userBucket {
	b, ok := tb.users[userID]
	if !ok {
		b = &userBucket{
			tokens:     float64(tb.capacity),
			lastRefill: time.Now(),
		}
		tb.users[userID] = b
	}
	return b
}

func (tb *TokenBucket) refill(b *userBucket) {
	now := time.Now()
	elapsed := now.Sub(b.lastRefill).Seconds()
	newTokens := b.tokens + elapsed*tb.refillRate
	if newTokens > float64(tb.capacity) {
		newTokens = float64(tb.capacity)
	}
	b.tokens = newTokens
	b.lastRefill = now
}

func (tb *TokenBucket) Allow(userID string) (bool, int, error) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	b := tb.bucketFor(userID)
	tb.refill(b)
	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		return true, int(b.tokens), nil
	}
	return false, 0, nil
}
