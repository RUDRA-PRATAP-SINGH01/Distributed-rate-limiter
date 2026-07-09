package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/idempotency"
)

func TestAdminIdempotencyAPI(t *testing.T) {
	fixture, cleanup := newTestFixture(t, nil)
	defer cleanup()

	// Seed an idempotency record in Redis
	store := idempotency.NewRedisStore(fixture.rdb, idempotency.DefaultConfig())
	ctx := context.Background()
	scope := idempotency.ScopeForTenantUser("tenant1", "user1")
	key := "pay-001"
	hash := "hash-1"

	claim, err := store.Claim(ctx, scope, key, hash)
	if err != nil {
		t.Fatalf("failed to seed idempotency claim: %v", err)
	}

	// 1. GET specific record via route path '/admin/idempotency/{scope}/{key}'
	reqGet, _ := http.NewRequest(http.MethodGet, fixture.adminURL+"/admin/idempotency/"+scope+"/"+key, nil)
	reqGet.Header.Set("X-API-Key", fixture.cfg.AdminAPIKey)
	respGet, err := http.DefaultClient.Do(reqGet)
	if err != nil {
		t.Fatalf("GET record failed: %v", err)
	}
	defer respGet.Body.Close()

	if respGet.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", respGet.StatusCode)
	}

	// 2. GET convenience lookup: ?tenant=tenant1&user=user1&key=pay-001
	reqLookup, _ := http.NewRequest(http.MethodGet, fixture.adminURL+"/admin/idempotency?tenant=tenant1&user=user1&key="+key, nil)
	reqLookup.Header.Set("X-API-Key", fixture.cfg.AdminAPIKey)
	respLookup, err := http.DefaultClient.Do(reqLookup)
	if err != nil {
		t.Fatalf("GET lookup failed: %v", err)
	}
	defer respLookup.Body.Close()

	if respLookup.StatusCode != http.StatusOK {
		t.Errorf("expected 200 on lookup, got %d", respLookup.StatusCode)
	}

	// 3. DELETE record
	reqDel, _ := http.NewRequest(http.MethodDelete, fixture.adminURL+"/admin/idempotency/"+scope+"/"+key, nil)
	reqDel.Header.Set("X-API-Key", fixture.cfg.AdminAPIKey)
	respDel, err := http.DefaultClient.Do(reqDel)
	if err != nil {
		t.Fatalf("DELETE record failed: %v", err)
	}
	defer respDel.Body.Close()

	if respDel.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", respDel.StatusCode)
	}

	// Verify record is gone
	rec, _ := store.GetRecord(ctx, scope, key)
	if rec != nil {
		t.Error("expected idempotency record to be deleted, but it still exists")
	}

	_ = claim
}
