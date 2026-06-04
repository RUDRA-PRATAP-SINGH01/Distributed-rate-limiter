package main

import (
    "sync"
    "time"
)

type TokenBucket struct {
    capacity   int
    tokens     float64
    refillRate float64
    lastRefill time.Time
    mu         sync.Mutex
}

func NewTokenBucket(capacity int, refillRate float64) *TokenBucket {
    return &TokenBucket{
        capacity:   capacity,
        tokens:     float64(capacity),
        refillRate: refillRate,
        lastRefill: time.Now(),
    }
}

func (tb *TokenBucket) refill() {
    now := time.Now()
    elapsed := now.Sub(tb.lastRefill).Seconds()
    newTokens := tb.tokens + elapsed*tb.refillRate
    if newTokens > float64(tb.capacity) {
        newTokens = float64(tb.capacity)
    }
    tb.tokens = newTokens
    tb.lastRefill = now
}

func (tb *TokenBucket) Allow(userID string) (bool, int, error) {
    tb.mu.Lock()
    defer tb.mu.Unlock()
    tb.refill()
    if tb.tokens >= 1.0 {
        tb.tokens -= 1.0
        return true, int(tb.tokens), nil
    }
    return false, 0, nil
}