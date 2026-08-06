//go:build smoke

// Package smoke is a shallow black-box suite against a running compose stack.
// It answers "is the system up?" — not "is the last change correct?"
package smoke

import (
	"context"
	"encoding/json"
	"strings"
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

func TestSmoke_LimiterHealth(t *testing.T) {
	c := qa.FromEnv()
	requireStack(t, c)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := c.HealthLimiter(ctx)
	if err != nil {
		t.Fatalf("limiter /health: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("limiter /health status=%d body=%s", resp.Status, resp.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("limiter /health json: %v", err)
	}
	if body["status"] != "healthy" {
		t.Fatalf("limiter status=%v body=%s", body["status"], resp.Body)
	}
}

func TestSmoke_SidecarHealth(t *testing.T) {
	c := qa.FromEnv()
	requireStack(t, c)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := c.HealthSidecar(ctx)
	if err != nil {
		t.Fatalf("sidecar /health: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("sidecar /health status=%d body=%s", resp.Status, resp.Body)
	}
	if !strings.Contains(string(resp.Body), "healthy") {
		t.Fatalf("sidecar /health body missing healthy: %s", resp.Body)
	}
}

func TestSmoke_LimiterCheckAnswers(t *testing.T) {
	c := qa.FromEnv()
	requireStack(t, c)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := c.Check(ctx, qa.UniqueUser("smoke"))
	if err != nil {
		t.Fatalf("/check: %v", err)
	}
	switch resp.Status {
	case 200, 429:
		// Either is a live limiter. 5xx means the process is up but broken.
	default:
		t.Fatalf("/check unexpected status=%d body=%s", resp.Status, resp.Body)
	}
}

func TestSmoke_SidecarProxyAnswers(t *testing.T) {
	c := qa.FromEnv()
	requireStack(t, c)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := c.SidecarProxy(ctx, qa.UniqueUser("smoke-sc"))
	if err != nil {
		t.Fatalf("sidecar /: %v", err)
	}
	if resp.Status >= 500 {
		t.Fatalf("sidecar / infra error status=%d body=%s", resp.Status, resp.Body)
	}
}
