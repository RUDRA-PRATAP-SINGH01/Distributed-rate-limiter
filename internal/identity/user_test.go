package identity

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveUserID_HeaderOnlyWhenQueryDisabled(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/check?user_id=query-user", nil)
	if _, err := ResolveUserID(req, false); err == nil {
		t.Fatal("expected missing identity when query is disabled")
	}
	req.Header.Set(UserIDHeader, "header-user")
	got, err := ResolveUserID(req, false)
	if err != nil || got != "header-user" {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestResolveTenantID_QueryGatedAndValidated(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/check_hierarchical?tenant_id=from-query", nil)
	got, err := ResolveTenantID(req, false)
	if err != nil || got != DefaultTenant {
		t.Fatalf("query tenant must be ignored when gated off, got %q %v", got, err)
	}

	got, err = ResolveTenantID(req, true)
	if err != nil || got != "from-query" {
		t.Fatalf("query tenant allowed, got %q %v", got, err)
	}

	req = httptest.NewRequest(http.MethodGet, "/check_hierarchical", nil)
	req.Header.Set(TenantIDHeader, "acme-prod")
	got, err = ResolveTenantID(req, false)
	if err != nil || got != "acme-prod" {
		t.Fatalf("header tenant, got %q %v", got, err)
	}

	req = httptest.NewRequest(http.MethodGet, "/check_hierarchical?tenant_id=ignored", nil)
	req.Header.Set(TenantIDHeader, "header-wins")
	got, err = ResolveTenantID(req, true)
	if err != nil || got != "header-wins" {
		t.Fatalf("header must beat query, got %q %v", got, err)
	}

	req = httptest.NewRequest(http.MethodGet, "/check_hierarchical", nil)
	req.Header.Set(TenantIDHeader, "bad tenant")
	if _, err := ResolveTenantID(req, false); err == nil {
		t.Fatal("expected invalid tenant id")
	}

	req = httptest.NewRequest(http.MethodGet, "/check_hierarchical", nil)
	req.Header.Set(TenantIDHeader, "inject{audit}")
	if _, err := ResolveTenantID(req, false); err == nil {
		t.Fatal("expected hash-tag injection rejected")
	}

	req = httptest.NewRequest(http.MethodGet, "/check_hierarchical", nil)
	req.Header.Set(TenantIDHeader, strings.Repeat("a", MaxIDLength+1))
	if _, err := ResolveTenantID(req, false); err == nil {
		t.Fatal("expected oversize tenant rejected")
	}
}
