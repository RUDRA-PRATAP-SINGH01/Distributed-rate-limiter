package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestAdminCircuitAPI(t *testing.T) {
	fixture, cleanup := newTestFixture(t, nil)
	defer cleanup()

	// 1. GET circuit snapshots list
	reqList, _ := http.NewRequest(http.MethodGet, fixture.adminURL+"/admin/circuit", nil)
	reqList.Header.Set("X-API-Key", fixture.cfg.AdminAPIKey)
	respList, err := http.DefaultClient.Do(reqList)
	if err != nil {
		t.Fatalf("GET list failed: %v", err)
	}
	defer respList.Body.Close()

	if respList.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", respList.StatusCode)
	}

	// 2. GET specific target 'redis'
	reqGet, _ := http.NewRequest(http.MethodGet, fixture.adminURL+"/admin/circuit/redis", nil)
	reqGet.Header.Set("X-API-Key", fixture.cfg.AdminAPIKey)
	respGet, err := http.DefaultClient.Do(reqGet)
	if err != nil {
		t.Fatalf("GET specific failed: %v", err)
	}
	defer respGet.Body.Close()

	if respGet.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", respGet.StatusCode)
	}

	var snap map[string]interface{}
	json.NewDecoder(respGet.Body).Decode(&snap)
	if snap["target"] != "redis" {
		t.Errorf("expected target 'redis', got %v", snap["target"])
	}

	// 3. DELETE (reset target 'redis')
	reqDel, _ := http.NewRequest(http.MethodDelete, fixture.adminURL+"/admin/circuit/redis", nil)
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
