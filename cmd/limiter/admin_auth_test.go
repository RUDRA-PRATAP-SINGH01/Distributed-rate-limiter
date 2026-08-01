package main

import (
	"bytes"
	"net/http"
	"testing"
)

func TestAdmin_Authentication(t *testing.T) {
	fixture, cleanup := newTestFixture(t, nil)
	defer cleanup()

	// List of test targets to check auth across admin API
	routes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/admin/limits/user/alice", ""},
		{http.MethodPost, "/admin/limits/user/alice", `{"capacity":20}`},
		{http.MethodDelete, "/admin/limits/user/alice", ""},
		{http.MethodGet, "/admin/limits/global/", ""},
		{http.MethodPost, "/admin/limits/global/", `{"capacity":50}`},

		{http.MethodGet, "/admin/idempotency/scope1/key1", ""},
		{http.MethodDelete, "/admin/idempotency/scope1/key1", ""},
		{http.MethodGet, "/admin/idempotency?user=alice&key=key1", ""},

		{http.MethodGet, "/admin/routing/gateways", ""},
		{http.MethodGet, "/admin/routing/gateways/gw1", ""},
		{http.MethodPost, "/admin/routing/gateways/gw1", `{"weight":10}`},
		{http.MethodDelete, "/admin/routing/gateways/gw1", ""},

		{http.MethodGet, "/admin/circuit", ""},
		{http.MethodGet, "/admin/circuit/redis", ""},
		{http.MethodDelete, "/admin/circuit/redis", ""},

		{http.MethodGet, "/admin/audit/stats", ""},
		{http.MethodGet, "/admin/audit/replay?id=123", ""},
		{http.MethodGet, "/admin/audit", ""},
		{http.MethodGet, "/admin/audit/123", ""},
		{http.MethodGet, "/admin/audit/123/replay", ""},
	}

	invalidKeys := []string{
		"",                     // Missing
		"wrong-key",            // Wrong key
		"test-admin-key-extra", // Prefix-correct
		"extra-test-admin-key", // Suffix-correct
		"test-admn-key",        // Same length (almost correct)
		"test-admin key",       // Internal whitespace mutation
		"test-admin-Key",       // Case mutation
	}

	for _, route := range routes {
		for _, key := range invalidKeys {
			t.Run(route.method+"_"+route.path+"_key_"+key, func(t *testing.T) {
				// Record Redis snapshot before request
				snapBefore := captureRedisSnapshot(t, fixture.rdb)

				req, err := http.NewRequest(route.method, fixture.adminURL+route.path, bytes.NewBufferString(route.body))
				if err != nil {
					t.Fatalf("failed to create request: %v", err)
				}
				if key != "" {
					req.Header.Set("X-API-Key", key)
				}

				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatalf("failed to perform request: %v", err)
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusUnauthorized {
					t.Errorf("expected 401 Unauthorized for route %s %s with key %q, got %d", route.method, route.path, key, resp.StatusCode)
				}

				// Record Redis snapshot after request
				snapAfter := captureRedisSnapshot(t, fixture.rdb)

				// Assert no mutation
				assertRedisSnapshotEqual(t, snapBefore, snapAfter)
			})
		}

		t.Run(route.method+"_"+route.path+"_correct_key", func(t *testing.T) {
			req, err := http.NewRequest(route.method, fixture.adminURL+route.path, bytes.NewBufferString(route.body))
			if err != nil {
				t.Fatalf("failed to create request: %v", err)
			}
			req.Header.Set("X-API-Key", fixture.cfg.AdminAPIKey)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("failed to perform request: %v", err)
			}
			defer resp.Body.Close()

			// Correct credentials should NOT return 401
			if resp.StatusCode == http.StatusUnauthorized {
				t.Errorf("expected access allowed (non-401) for route %s %s with correct key, got %d", route.method, route.path, resp.StatusCode)
			}
		})
	}
}
