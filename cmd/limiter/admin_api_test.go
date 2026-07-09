package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestAdminAPI_CRUD(t *testing.T) {
	fixture, cleanup := newTestFixture(t, nil)
	defer cleanup()

	// 1. GET non-existent override: should return default values and X-Override-Applied: false
	reqGet, _ := http.NewRequest(http.MethodGet, fixture.adminURL+"/admin/limits/user/alice", nil)
	reqGet.Header.Set("X-API-Key", fixture.cfg.AdminAPIKey)
	respGet, err := http.DefaultClient.Do(reqGet)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer respGet.Body.Close()

	if respGet.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", respGet.StatusCode)
	}
	if respGet.Header.Get("X-Override-Applied") != "false" {
		t.Errorf("expected X-Override-Applied: false, got %q", respGet.Header.Get("X-Override-Applied"))
	}

	// 2. POST valid override: capacity=45, refill_rate=2.5
	bodyPost := `{"capacity": 45, "refill_rate": 2.5}`
	reqPost, _ := http.NewRequest(http.MethodPost, fixture.adminURL+"/admin/limits/user/alice", bytes.NewBufferString(bodyPost))
	reqPost.Header.Set("X-API-Key", fixture.cfg.AdminAPIKey)
	respPost, err := http.DefaultClient.Do(reqPost)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer respPost.Body.Close()

	if respPost.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204 NoContent, got %d", respPost.StatusCode)
	}

	// Verify exact Redis state change (hash key should match "config:user:alice")
	data, err := fixture.rdb.HGetAll(context.Background(), "config:user:alice").Result()
	if err != nil {
		t.Fatalf("failed to get Redis override: %v", err)
	}
	if data["capacity"] != "45" || data["refill_rate"] != "2.5" {
		t.Errorf("stored values don't match: got cap=%s, rate=%s", data["capacity"], data["refill_rate"])
	}

	// 3. GET override again: should return applied=true and values
	reqGet2, _ := http.NewRequest(http.MethodGet, fixture.adminURL+"/admin/limits/user/alice", nil)
	reqGet2.Header.Set("X-API-Key", fixture.cfg.AdminAPIKey)
	respGet2, err := http.DefaultClient.Do(reqGet2)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer respGet2.Body.Close()

	if respGet2.Header.Get("X-Override-Applied") != "true" {
		t.Errorf("expected X-Override-Applied: true, got %q", respGet2.Header.Get("X-Override-Applied"))
	}
	var retrieved struct {
		Capacity   int     `json:"capacity"`
		RefillRate float64 `json:"refill_rate"`
	}
	json.NewDecoder(respGet2.Body).Decode(&retrieved)
	if retrieved.Capacity != 45 || retrieved.RefillRate != 2.5 {
		t.Errorf("retrieved values don't match: got cap=%d, rate=%f", retrieved.Capacity, retrieved.RefillRate)
	}

	// 4. DELETE override
	reqDel, _ := http.NewRequest(http.MethodDelete, fixture.adminURL+"/admin/limits/user/alice", nil)
	reqDel.Header.Set("X-API-Key", fixture.cfg.AdminAPIKey)
	respDel, err := http.DefaultClient.Do(reqDel)
	if err != nil {
		t.Fatalf("DELETE failed: %v", err)
	}
	defer respDel.Body.Close()

	if respDel.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", respDel.StatusCode)
	}

	// Verify deleted from Redis
	exists, _ := fixture.rdb.Exists(context.Background(), "config:user:alice").Result()
	if exists > 0 {
		t.Error("expected override key to be deleted from Redis, but it still exists")
	}
}

func TestAdminAPI_MalformedInputs(t *testing.T) {
	fixture, cleanup := newTestFixture(t, nil)
	defer cleanup()

	tests := []struct {
		name string
		body string
	}{
		{"empty_json", ""},
		{"unclosed_brace", "{"},
		{"null_json", "null"},
		{"negative_capacity", `{"capacity":-5}`},
		{"zero_capacity", `{"capacity":0}`},
		{"wrong_type", `{"capacity":"abc"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, fixture.adminURL+"/admin/limits/user/alice", bytes.NewBufferString(tt.body))
			req.Header.Set("X-API-Key", fixture.cfg.AdminAPIKey)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("POST failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("expected HTTP 400 Bad Request, got %d", resp.StatusCode)
			}
		})
	}
}
