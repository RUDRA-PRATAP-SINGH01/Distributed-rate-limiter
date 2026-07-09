package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

func TestCheckHandler_TokenBucket(t *testing.T) {
	fixture, cleanup := newTestFixture(t, func(c *Config) {
		c.Algorithm = "token"
		c.Capacity = 3
		c.RefillRate = 0.1 // very slow refill so it's deterministic
	})
	defer cleanup()

	// 1. Successive requests within capacity
	for i := 0; i < 3; i++ {
		req, err := http.NewRequest(http.MethodGet, fixture.mainURL+"/check", nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		req.Header.Set("X-API-Key", fixture.cfg.InternalAPIKey)
		req.Header.Set("X-User-ID", "alice-token")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to perform request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected HTTP 200 OK, got %d", resp.StatusCode)
		}

		var body map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode JSON response: %v", err)
		}
		if body["allowed"] != true {
			t.Errorf("expected allowed=true, got allowed=%v", body["allowed"])
		}

		remHeader := resp.Header.Get("X-RateLimit-Remaining")
		if remHeader == "" {
			t.Error("missing X-RateLimit-Remaining header")
		}
	}

	// 2. Denied request (Limit Exceeded)
	req, err := http.NewRequest(http.MethodGet, fixture.mainURL+"/check", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("X-API-Key", fixture.cfg.InternalAPIKey)
	req.Header.Set("X-User-ID", "alice-token")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to perform request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected HTTP 429 Too Many Requests, got %d", resp.StatusCode)
	}

	retryAfter := resp.Header.Get("Retry-After")
	if retryAfter == "" {
		t.Error("expected Retry-After header on 429 response")
	}

	// 3. Prove correct algorithm key in Redis (rate:alice-token)
	exists, err := fixture.rdb.Exists(context.Background(), "rate:alice-token").Result()
	if err != nil {
		t.Fatalf("failed to check Redis key: %v", err)
	}
	if exists == 0 {
		t.Error("expected Token Bucket key 'rate:alice-token' to exist in Redis, but it does not")
	}
}

func TestCheckHandler_SlidingWindow(t *testing.T) {
	fixture, cleanup := newTestFixture(t, func(c *Config) {
		c.Algorithm = "sliding"
		c.Capacity = 2
		c.WindowSec = 60
	})
	defer cleanup()

	for i := 0; i < 2; i++ {
		req, err := http.NewRequest(http.MethodGet, fixture.mainURL+"/check", nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		req.Header.Set("X-API-Key", fixture.cfg.InternalAPIKey)
		req.Header.Set("X-User-ID", "bob-sliding")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to perform request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected HTTP 200 OK, got %d", resp.StatusCode)
		}
	}

	// Denied request
	req, err := http.NewRequest(http.MethodGet, fixture.mainURL+"/check", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("X-API-Key", fixture.cfg.InternalAPIKey)
	req.Header.Set("X-User-ID", "bob-sliding")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to perform request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected HTTP 429, got %d", resp.StatusCode)
	}

	// Prove correct algorithm key in Redis (sw:bob-sliding)
	exists, err := fixture.rdb.Exists(context.Background(), "sw:bob-sliding").Result()
	if err != nil {
		t.Fatalf("failed to check Redis key: %v", err)
	}
	if exists == 0 {
		t.Error("expected Sliding Window key 'sw:bob-sliding' to exist in Redis, but it does not")
	}
}

func TestCheckHandler_BadRequests(t *testing.T) {
	fixture, cleanup := newTestFixture(t, nil)
	defer cleanup()

	t.Run("missing_identity", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, fixture.mainURL+"/check", nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		req.Header.Set("X-API-Key", fixture.cfg.InternalAPIKey)
		// Missing X-User-ID header

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to perform request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected HTTP 400 Bad Request, got %d", resp.StatusCode)
		}
	})

	t.Run("duplicate_query_params", func(t *testing.T) {
		// If query params are duplicate, ResolveUserID might use the first or error
		req, err := http.NewRequest(http.MethodGet, fixture.mainURL+"/check?user_id=alice&user_id=bob", nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		req.Header.Set("X-API-Key", fixture.cfg.InternalAPIKey)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to perform request: %v", err)
		}
		defer resp.Body.Close()

		// Should either error (400) or resolve to one. Let's verify actual behavior.
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 200 or 400, got %d", resp.StatusCode)
		}
	})

	t.Run("malformed_url_encoding", func(t *testing.T) {
		// Path with percent signs that aren't followed by hex
		u, _ := url.Parse(fixture.mainURL + "/check?user_id=%zz")
		req := &http.Request{
			Method: http.MethodGet,
			URL:    u,
			Header: make(http.Header),
		}
		req.Header.Set("X-API-Key", fixture.cfg.InternalAPIKey)

		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("expected HTTP 400 on malformed URL encoding, got %d", resp.StatusCode)
			}
		}
	})
}
