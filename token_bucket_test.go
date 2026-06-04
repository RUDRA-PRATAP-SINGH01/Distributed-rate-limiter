package main

import (
    "testing"
    "time"
)

func TestTokenBucket_AllowWithinCapacity(t *testing.T) {
    tb := NewTokenBucket(3, 1.0)
    for i := 0; i < 3; i++ {
        if allowed, _, _ := tb.Allow("test"); !allowed {
            t.Errorf("request %d should be allowed", i)
        }
    }
    if allowed, _, _ := tb.Allow("test"); allowed {
        t.Error("4th request should be denied")
    }
}

func TestTokenBucket_Refill(t *testing.T) {
    tb := NewTokenBucket(3, 1.0)
    tb.Allow("test")
    tb.Allow("test")
    tb.Allow("test")
    time.Sleep(2 * time.Second)
    if !tb.Allow("test") {
        t.Error("after refill, request should be allowed")
    }
}