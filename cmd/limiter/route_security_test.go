package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRouteSecurity_MainEndpointsAuth(t *testing.T) {
	fixture, cleanup := newTestFixture(t, nil)
	defer cleanup()

	routes := []struct {
		path   string
		keyEnv string
		keyVal string
	}{
		{"/check?user_id=alice", "INTERNAL_API_KEY", "test-internal-key"},
		{"/check_hierarchical?user_id=alice", "INTERNAL_API_KEY", "test-internal-key"},
		{"/metrics", "METRICS_API_KEY", "test-metrics-key"},
	}

	invalidKeys := []string{
		"",               // Missing
		"wrong-key",      // Wrong key
		"test-key-extra", // Prefix-correct (we'll customize per test)
		"extra-test-key", // Suffix-correct
		"test-ke-almost", // Same length
		"test-key-case",  // Case mutation
		"test-key space", // Internal whitespace mutation
	}

	for _, route := range routes {
		t.Run(route.path, func(t *testing.T) {
			// 1. Check with invalid keys
			for _, k := range invalidKeys {
				actualKey := k
				if k == "test-key-extra" {
					actualKey = route.keyVal + "-extra"
				} else if k == "extra-test-key" {
					actualKey = "extra-" + route.keyVal
				} else if k == "test-ke-almost" {
					actualKey = route.keyVal[:len(route.keyVal)-1] + "x"
				} else if k == "test-key-case" {
					actualKey = strings.ToUpper(route.keyVal[:1]) + route.keyVal[1:]
				} else if k == "test-key space" {
					mid := len(route.keyVal) / 2
					actualKey = route.keyVal[:mid] + " " + route.keyVal[mid:]
				}

				req, err := http.NewRequest(http.MethodGet, fixture.mainURL+route.path, nil)
				if err != nil {
					t.Fatalf("failed to create request: %v", err)
				}
				if actualKey != "" {
					req.Header.Set("X-API-Key", actualKey)
				}

				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatalf("failed to perform request: %v", err)
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusUnauthorized {
					t.Errorf("expected 401 Unauthorized for route %s with key %q, got %d", route.path, actualKey, resp.StatusCode)
				}
			}

			// 2. Check with correct key
			req, err := http.NewRequest(http.MethodGet, fixture.mainURL+route.path, nil)
			if err != nil {
				t.Fatalf("failed to create request: %v", err)
			}
			req.Header.Set("X-API-Key", route.keyVal)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("failed to perform request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusUnauthorized {
				t.Errorf("expected non-401 for route %s with correct key, got %d", route.path, resp.StatusCode)
			}
		})
	}
}

func TestRouteSecurity_PublicEndpoints(t *testing.T) {
	fixture, cleanup := newTestFixture(t, nil)
	defer cleanup()

	req, err := http.NewRequest(http.MethodGet, fixture.mainURL+"/health", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to perform request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected public /health to return 200 OK, got %d", resp.StatusCode)
	}
}

func TestRouteSecurity_UnsupportedMethods(t *testing.T) {
	fixture, cleanup := newTestFixture(t, nil)
	defer cleanup()

	routes := []struct {
		server *httptest.Server
		method string
		path   string
		key    string
	}{
		{fixture.adminSrv, http.MethodPost, "/admin/idempotency", fixture.cfg.AdminAPIKey},
		{fixture.adminSrv, http.MethodPut, "/admin/limits/user/alice", fixture.cfg.AdminAPIKey},
		{fixture.adminSrv, http.MethodPost, "/admin/routing/gateways", fixture.cfg.AdminAPIKey},
		{fixture.adminSrv, http.MethodPut, "/admin/circuit/redis", fixture.cfg.AdminAPIKey},
		{fixture.adminSrv, http.MethodPost, "/admin/audit", fixture.cfg.AdminAPIKey},
	}

	for _, route := range routes {
		t.Run(route.path, func(t *testing.T) {
			req, err := http.NewRequest(route.method, route.server.URL+route.path, nil)
			if err != nil {
				t.Fatalf("failed to create request: %v", err)
			}
			req.Header.Set("X-API-Key", route.key)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("failed to perform request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("expected 405 Method Not Allowed for path %s, got %d", route.path, resp.StatusCode)
			}
		})
	}
}

func TestRouteSecurity_UnknownRoutes(t *testing.T) {
	fixture, cleanup := newTestFixture(t, nil)
	defer cleanup()

	servers := []struct {
		name string
		srv  *httptest.Server
		key  string
	}{
		{"main", fixture.mainSrv, "test-internal-key"},
		{"admin", fixture.adminSrv, "test-admin-key"},
	}

	for _, s := range servers {
		t.Run(s.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, s.srv.URL+"/unknown-route", nil)
			if err != nil {
				t.Fatalf("failed to create request: %v", err)
			}
			req.Header.Set("X-API-Key", s.key)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("failed to perform request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("expected 404 Not Found for unknown route, got %d", resp.StatusCode)
			}
		})
	}
}
