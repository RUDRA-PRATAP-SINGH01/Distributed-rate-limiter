package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHealthEndpoint_Healthy(t *testing.T) {
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
		t.Errorf("expected HTTP 200 OK, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	if body["status"] != "healthy" {
		t.Errorf("expected status=healthy, got %v", body["status"])
	}
}

func TestHealthEndpoint_Unhealthy(t *testing.T) {
	fixture, cleanup := newTestFixture(t, nil)
	defer cleanup()

	// Close Redis client to force unhealthy state
	fixture.rdb.Close()

	req, err := http.NewRequest(http.MethodGet, fixture.mainURL+"/health", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to perform request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected HTTP 503 Service Unavailable, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	if body["status"] != "unhealthy" {
		t.Errorf("expected status=unhealthy, got %v", body["status"])
	}
}

func TestMetricsEndpoint(t *testing.T) {
	fixture, cleanup := newTestFixture(t, nil)
	defer cleanup()

	req, err := http.NewRequest(http.MethodGet, fixture.mainURL+"/metrics", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("X-API-Key", fixture.cfg.MetricsAuthKey())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to perform request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200 OK, got %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	metricsStr := string(bodyBytes)

	// 1. Assert no leaked admin API key or secret in metrics output
	if strings.Contains(metricsStr, fixture.cfg.AdminAPIKey) {
		t.Error("Metrics endpoint leaked ADMIN_API_KEY!")
	}
	if strings.Contains(metricsStr, fixture.cfg.InternalAPIKey) {
		t.Error("Metrics endpoint leaked INTERNAL_API_KEY!")
	}

	// 2. Assert no user ID labels exist in rate limiter metrics (low cardinality enforcement)
	if strings.Contains(metricsStr, "user_id") || strings.Contains(metricsStr, "userid") {
		t.Error("Metrics endpoint contains high-cardinality user ID labels")
	}
}
