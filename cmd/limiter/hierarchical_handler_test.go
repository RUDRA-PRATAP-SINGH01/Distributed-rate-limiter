package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestHierarchicalHandler_Basic(t *testing.T) {
	fixture, cleanup := newTestFixture(t, func(c *Config) {
		c.EnableHierarchical = true
		c.GlobalCapacity = 100
		c.TenantCapacity = 50
		c.UserCapacity = 5
		c.EndpointCapacity = 2
	})
	defer cleanup()

	// 1. Successive calls within capacity limits
	for i := 0; i < 2; i++ {
		req, err := http.NewRequest(http.MethodGet, fixture.mainURL+"/check_hierarchical?endpoint=/api/v1/resource", nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		req.Header.Set("X-API-Key", fixture.cfg.InternalAPIKey)
		req.Header.Set("X-User-ID", "alice-hier")
		req.Header.Set("X-Tenant-ID", "tenant-1")

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
	}

	// 2. Denied request due to Endpoint limit (capacity = 2)
	req, err := http.NewRequest(http.MethodGet, fixture.mainURL+"/check_hierarchical?endpoint=/api/v1/resource", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("X-API-Key", fixture.cfg.InternalAPIKey)
	req.Header.Set("X-User-ID", "alice-hier")
	req.Header.Set("X-Tenant-ID", "tenant-1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to perform request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected HTTP 429 Too Many Requests, got %d", resp.StatusCode)
	}

	// 3. Namespace isolation: another user or endpoint under the same tenant should still have budget
	reqOther, err := http.NewRequest(http.MethodGet, fixture.mainURL+"/check_hierarchical?endpoint=/api/v2/other", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	reqOther.Header.Set("X-API-Key", fixture.cfg.InternalAPIKey)
	reqOther.Header.Set("X-User-ID", "alice-hier")
	reqOther.Header.Set("X-Tenant-ID", "tenant-1")

	respOther, err := http.DefaultClient.Do(reqOther)
	if err != nil {
		t.Fatalf("failed to perform request: %v", err)
	}
	defer respOther.Body.Close()

	if respOther.StatusCode != http.StatusOK {
		t.Errorf("expected other endpoint to be allowed (HTTP 200), got %d", respOther.StatusCode)
	}
}

func TestHierarchicalHandler_DefaultFallbacks(t *testing.T) {
	fixture, cleanup := newTestFixture(t, func(c *Config) {
		c.EnableHierarchical = true
	})
	defer cleanup()

	// Missing tenant ID and endpoint path should fall back to defaults
	req, err := http.NewRequest(http.MethodGet, fixture.mainURL+"/check_hierarchical", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("X-API-Key", fixture.cfg.InternalAPIKey)
	req.Header.Set("X-User-ID", "user-default-hier")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to perform request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200 OK with defaults, got %d", resp.StatusCode)
	}

	// Verify defaults keys exist in Redis: rate:tenant:default and rate:endpoint:default:default
	existsTenant, _ := fixture.rdb.Exists(context.Background(), "rate:tenant:default").Result()
	existsEndpoint, _ := fixture.rdb.Exists(context.Background(), "rate:endpoint:default:default").Result()

	if existsTenant == 0 {
		t.Error("expected tenant key 'rate:tenant:default' to exist in Redis")
	}
	if existsEndpoint == 0 {
		t.Error("expected endpoint key 'rate:endpoint:default:default' to exist in Redis")
	}
}

func TestHierarchicalHandler_QueryTenantIgnoredWhenDisallowed(t *testing.T) {
	fixture, cleanup := newTestFixture(t, func(c *Config) {
		c.EnableHierarchical = true
		c.AllowQueryUserID = false
		c.GlobalCapacity = 1000
		c.TenantCapacity = 2
		c.UserCapacity = 100
		c.EndpointCapacity = 100
	})
	defer cleanup()

	do := func(user, tenantHeader, rawQuery string) int {
		u := fixture.mainURL + "/check_hierarchical?endpoint=/api/x"
		if rawQuery != "" {
			u += "&" + rawQuery
		}
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-API-Key", fixture.cfg.InternalAPIKey)
		req.Header.Set("X-User-ID", user)
		if tenantHeader != "" {
			req.Header.Set("X-Tenant-ID", tenantHeader)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	if got := do("alice", "victim", ""); got != http.StatusOK {
		t.Fatalf("seed 1: got %d", got)
	}
	if got := do("alice", "victim", ""); got != http.StatusOK {
		t.Fatalf("seed 2: got %d", got)
	}
	if got := do("alice", "victim", ""); got != http.StatusTooManyRequests {
		t.Fatalf("victim tenant should be exhausted, got %d", got)
	}
	if got := do("bob", "", "tenant_id=victim"); got != http.StatusOK {
		t.Fatalf("?tenant_id= must be ignored when ALLOW_QUERY_USER_ID=false, got %d", got)
	}
	if got := do("carol", "bad tenant", ""); got != http.StatusBadRequest {
		t.Fatalf("invalid tenant header must 400, got %d", got)
	}
}
