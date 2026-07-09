package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/audit"
)

func TestAdminAuditAPI(t *testing.T) {
	fixture, cleanup := newTestFixture(t, nil)
	defer cleanup()

	// Seed audit trail entries using auditStore
	ctx := context.Background()
	auditCfg := audit.DefaultConfig()
	auditCfg.Async = false
	store := audit.NewStore(fixture.rdb, auditCfg)

	in := audit.RecordInput{
		RequestID: "req-1",
		TenantID:  "tenant1",
		UserID:    "user1",
		Decision:  audit.DecisionAllowed,
		Reason:    "allowed",
		Handler:   "check",
		Remaining: 5,
	}

	ev, err := store.Record(ctx, in)
	if err != nil {
		t.Fatalf("failed to record audit entry: %v", err)
	}

	// 1. GET search query '/admin/audit?user_id=user1'
	reqSearch, _ := http.NewRequest(http.MethodGet, fixture.adminURL+"/admin/audit?user_id=user1", nil)
	reqSearch.Header.Set("X-API-Key", fixture.cfg.AdminAPIKey)
	respSearch, err := http.DefaultClient.Do(reqSearch)
	if err != nil {
		t.Fatalf("GET search failed: %v", err)
	}
	defer respSearch.Body.Close()

	if respSearch.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", respSearch.StatusCode)
	}

	// 2. GET audit stats '/admin/audit/stats'
	reqStats, _ := http.NewRequest(http.MethodGet, fixture.adminURL+"/admin/audit/stats", nil)
	reqStats.Header.Set("X-API-Key", fixture.cfg.AdminAPIKey)
	respStats, err := http.DefaultClient.Do(reqStats)
	if err != nil {
		t.Fatalf("GET stats failed: %v", err)
	}
	defer respStats.Body.Close()

	if respStats.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", respStats.StatusCode)
	}

	// 3. GET specific event details '/admin/audit/{id}'
	reqGet, _ := http.NewRequest(http.MethodGet, fixture.adminURL+"/admin/audit/"+ev.ID, nil)
	reqGet.Header.Set("X-API-Key", fixture.cfg.AdminAPIKey)
	respGet, err := http.DefaultClient.Do(reqGet)
	if err != nil {
		t.Fatalf("GET event failed: %v", err)
	}
	defer respGet.Body.Close()

	if respGet.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", respGet.StatusCode)
	}

	// 4. GET specific event replay '/admin/audit/{id}/replay'
	reqReplay, _ := http.NewRequest(http.MethodGet, fixture.adminURL+"/admin/audit/"+ev.ID+"/replay", nil)
	reqReplay.Header.Set("X-API-Key", fixture.cfg.AdminAPIKey)
	respReplay, err := http.DefaultClient.Do(reqReplay)
	if err != nil {
		t.Fatalf("GET replay failed: %v", err)
	}
	defer respReplay.Body.Close()

	if respReplay.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", respReplay.StatusCode)
	}
}
