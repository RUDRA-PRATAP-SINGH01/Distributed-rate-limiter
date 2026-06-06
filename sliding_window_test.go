package main

import (
	"testing"
	"time"
)

func TestSlidingWindow_Allow(t *testing.T) {
	sw := NewSlidingWindow(3, 2*time.Second)
	user := "alice"

	for i := 0; i < 3; i++ {
		if allowed, _, _ := sw.Allow(user); !allowed {
			t.Errorf("request %d should be allowed", i)
		}
	}
	if allowed, _, _ := sw.Allow(user); allowed {
		t.Error("4th request should be denied")
	}
	time.Sleep(2 * time.Second)
	if allowed, _, _ := sw.Allow(user); !allowed {
		t.Error("after window expiration, request should be allowed")
	}
}
