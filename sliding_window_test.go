package main

import (
    "testing"
    "time"
)

func TestSlidingWindow_Allow(t *testing.T) {
    sw := NewSlidingWindow(3, 2*time.Second)
    user := "alice"

    for i := 0; i < 3; i++ {
        if !sw.Allow(user) {
            t.Errorf("request %d should be allowed", i)
        }
    }
    if sw.Allow(user) {
        t.Error("4th request should be denied")
    }
    time.Sleep(2 * time.Second)
    if !sw.Allow(user) {
        t.Error("after window expiration, request should be allowed")
    }
}