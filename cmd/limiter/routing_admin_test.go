package main

import (
	"bytes"
	"context"
	"net/http"
	"testing"
)

func TestAdminRoutingAPI(t *testing.T) {
	fixture, cleanup := newTestFixture(t, nil)
	defer cleanup()

	// 1. GET all gateways (should return list)
	reqGet, _ := http.NewRequest(http.MethodGet, fixture.adminURL+"/admin/routing/gateways", nil)
	reqGet.Header.Set("X-API-Key", fixture.cfg.AdminAPIKey)
	respGet, err := http.DefaultClient.Do(reqGet)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer respGet.Body.Close()

	if respGet.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", respGet.StatusCode)
	}

	// 2. POST update weight & enabled state for gateway 'gw1'
	bodyPost := `{"weight": 85, "enabled": true}`
	reqPost, _ := http.NewRequest(http.MethodPost, fixture.adminURL+"/admin/routing/gateways/gw1", bytes.NewBufferString(bodyPost))
	reqPost.Header.Set("X-API-Key", fixture.cfg.AdminAPIKey)
	respPost, err := http.DefaultClient.Do(reqPost)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer respPost.Body.Close()

	if respPost.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", respPost.StatusCode)
	}

	// Verify weight in Redis (key should be "route:gw:gw1" field "weight")
	wVal, _ := fixture.rdb.HGet(context.Background(), "route:gw:gw1", "weight").Result()
	if wVal != "85" {
		t.Errorf("expected weight=85 in Redis, got %q", wVal)
	}

	// 3. GET details for gateway 'gw1'
	reqGet2, _ := http.NewRequest(http.MethodGet, fixture.adminURL+"/admin/routing/gateways/gw1", nil)
	reqGet2.Header.Set("X-API-Key", fixture.cfg.AdminAPIKey)
	respGet2, err := http.DefaultClient.Do(reqGet2)
	if err != nil {
		t.Fatalf("GET details failed: %v", err)
	}
	defer respGet2.Body.Close()

	if respGet2.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", respGet2.StatusCode)
	}

	// 4. DELETE (reset circuit breaker for gw1)
	reqDel, _ := http.NewRequest(http.MethodDelete, fixture.adminURL+"/admin/routing/gateways/gw1", nil)
	reqDel.Header.Set("X-API-Key", fixture.cfg.AdminAPIKey)
	respDel, err := http.DefaultClient.Do(reqDel)
	if err != nil {
		t.Fatalf("DELETE failed: %v", err)
	}
	defer respDel.Body.Close()

	if respDel.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", respDel.StatusCode)
	}
}
