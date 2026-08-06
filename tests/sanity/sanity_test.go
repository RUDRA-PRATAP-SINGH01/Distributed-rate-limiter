//go:build sanity

// Package sanity is a narrow black-box suite against a running compose stack.
// It answers "did the last change break the happy path?" — not "is every
// package green?" and not "is the process merely listening?"
package sanity

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/tests/qa"
)

func requireStack(t *testing.T, c *qa.Client) {
	t.Helper()
	if c.StackReachable() {
		return
	}
	msg := "compose stack not reachable at " + c.LimiterURL + "/health — start with .\\scripts\\start.ps1 or docker compose up"
	if qa.RequireStack() {
		t.Fatal(msg)
	}
	t.Skip(msg)
}

func TestSanity_CheckRejectsMissingAPIKey(t *testing.T) {
	c := qa.FromEnv()
	requireStack(t, c)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := c.CheckNoAuth(ctx, qa.UniqueUser("sanity-unauth"))
	if err != nil {
		t.Fatalf("/check no-auth: %v", err)
	}
	if resp.Status != 401 {
		t.Fatalf("expected 401 without API key, got %d body=%s", resp.Status, resp.Body)
	}
}

func TestSanity_CheckAllowsFreshUser(t *testing.T) {
	c := qa.FromEnv()
	requireStack(t, c)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := c.Check(ctx, qa.UniqueUser("sanity-allow"))
	if err != nil {
		t.Fatalf("/check: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200 for a fresh user, got %d body=%s", resp.Status, resp.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("/check json: %v", err)
	}
	if body["allowed"] != true {
		t.Fatalf("expected allowed=true, got %v", body["allowed"])
	}
	if resp.Header.Get("X-RateLimit-Remaining") == "" {
		t.Fatal("missing X-RateLimit-Remaining on allow")
	}
}

func TestSanity_CheckEventuallyDenies(t *testing.T) {
	c := qa.FromEnv()
	requireStack(t, c)

	user := qa.UniqueUser("sanity-deny")
	// Compose default CAPACITY=10. Ask for 12 to be robust if refill happens.
	var last qa.Response
	for i := 0; i < 12; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		resp, err := c.Check(ctx, user)
		cancel()
		if err != nil {
			t.Fatalf("/check burst %d: %v", i, err)
		}
		last = resp
		if resp.Status == 429 {
			if resp.Header.Get("Retry-After") == "" {
				t.Fatal("429 missing Retry-After")
			}
			return
		}
		if resp.Status != 200 {
			t.Fatalf("/check burst %d unexpected status=%d body=%s", i, resp.Status, resp.Body)
		}
	}
	t.Fatalf("expected a 429 within 12 requests, last status=%d body=%s", last.Status, last.Body)
}

func TestSanity_SidecarProxiesFreshUser(t *testing.T) {
	c := qa.FromEnv()
	requireStack(t, c)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := c.SidecarProxy(ctx, qa.UniqueUser("sanity-proxy"))
	if err != nil {
		t.Fatalf("sidecar /: %v", err)
	}
	if resp.Status != 200 && resp.Status != 429 {
		t.Fatalf("sidecar / expected 200 or 429, got %d body=%s", resp.Status, resp.Body)
	}
}

func TestSanity_AdminRejectsMissingAPIKey(t *testing.T) {
	c := qa.FromEnv()
	requireStack(t, c)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := c.AdminUnauth(ctx)
	if err != nil {
		t.Fatalf("admin unauth: %v", err)
	}
	if resp.Status != 401 {
		t.Fatalf("expected admin 401 without key, got %d body=%s", resp.Status, resp.Body)
	}
}
