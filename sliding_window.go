package main

import (
    "sync"
    "time"
)

type SlidingWindow struct {
    limit    int
    window   time.Duration
    requests map[string][]time.Time
    mu       sync.Mutex
}

func NewSlidingWindow(limit int, window time.Duration) *SlidingWindow {
    return &SlidingWindow{
        limit:    limit,
        window:   window,
        requests: make(map[string][]time.Time),
    }
}

func (sw *SlidingWindow) Allow(userID string) (bool, int, error) {
    sw.mu.Lock()
    defer sw.mu.Unlock()

    now := time.Now()
    timestamps := sw.requests[userID]
    cutoff := now.Add(-sw.window)
    valid := []time.Time{}
    for _, ts := range timestamps {
        if ts.After(cutoff) {
            valid = append(valid, ts)
        }
    }
    if len(valid) < sw.limit {
        valid = append(valid, now)
        sw.requests[userID] = valid
        return true, sw.limit - len(valid), nil
    }
    sw.requests[userID] = valid
    return false, 0, nil
}