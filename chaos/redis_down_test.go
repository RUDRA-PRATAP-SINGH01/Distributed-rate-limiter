//go:build chaos

package chaos

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Contract R1: Redis down → fail-closed 503 (no secret leak) → recovery.
func TestContract_R1_RedisDown_FailClosed_Limiter(t *testing.T) {
	h := StartChaosStack(t)
	client := ClientFromEnv(h.LimiterURL)
	ctx := context.Background()

	user := uniqueUser("chaos")
	resp, err := client.Check(ctx, user)
	if err != nil {
		t.Fatalf("baseline check: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("baseline: want 200, got %d body=%s", resp.Status, string(resp.Body))
	}

	h.StopRedis()
	time.Sleep(1 * time.Second)

	resp, err = client.Check(ctx, user)
	if err != nil {
		t.Fatalf("redis-down check: %v", err)
	}
	if resp.Status != http.StatusServiceUnavailable {
		t.Fatalf("redis-down: want 503 fail-closed, got %d body=%s", resp.Status, string(resp.Body))
	}
	secretsMustNotLeak(t, resp.Body)
	if !strings.Contains(strings.ToLower(string(resp.Body)), "unavailable") &&
		!strings.Contains(string(resp.Body), "circuit_state") {
		t.Fatalf("redis-down: unexpected body (want unavailable/circuit): %s", string(resp.Body))
	}

	h.StartRedis()
	h.WaitLimiterHealthy(60 * time.Second)

	// Local Redis circuit stays open until cooldown + successful half-open probe(s).
	// Health can pass while /check is still rejected — poll for real recovery.
	if err := waitCheckOK(t, client, 45*time.Second); err != nil {
		t.Fatalf("recovery: %v", err)
	}
}

func waitCheckOK(t *testing.T, client *Client, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last Response
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		resp, err := client.Check(ctx, uniqueUser("recover"))
		cancel()
		last, lastErr = resp, err
		if err == nil && resp.Status == http.StatusOK {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	if lastErr != nil {
		return lastErr
	}
	return &recoveryError{status: last.Status, body: string(last.Body)}
}

type recoveryError struct {
	status int
	body   string
}

func (e *recoveryError) Error() string {
	return fmt.Sprintf("want 200 after Redis recovery (circuit cooldown/half-open); last status=%d body=%s", e.status, e.body)
}

// Contract R1 on the sidecar path: with FAIL_OPEN=false, Redis/limiter failure
// must surface as 503 to callers (production-shaped headers, not ?user_id=).
func TestContract_R1_RedisDown_FailClosed_Sidecar(t *testing.T) {
	h := StartChaosStack(t)
	sidecar := NewClient(h.SidecarURL, envOr("INTERNAL_API_KEY", "dev-internal-key-change-in-prod"))
	// Sidecar proxies / ; identity is still required for quota.
	ctx := context.Background()

	user := uniqueUser("sidecar-chaos")
	reqOK := func(userID string) (Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.SidecarURL+"/", nil)
		if err != nil {
			return Response{}, err
		}
		req.Header.Set("X-User-ID", userID)
		req.Header.Set("X-Internal-API-Key", sidecar.InternalAPIKey)
		return sidecar.do(req)
	}

	resp, err := reqOK(user)
	if err != nil {
		t.Fatalf("baseline sidecar: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("baseline sidecar: want 200, got %d body=%s", resp.Status, string(resp.Body))
	}

	h.StopRedis()
	time.Sleep(1 * time.Second)

	resp, err = reqOK(user)
	if err != nil {
		t.Fatalf("redis-down sidecar: %v", err)
	}
	if resp.Status != http.StatusServiceUnavailable {
		t.Fatalf("redis-down sidecar: want 503 fail-closed, got %d body=%s", resp.Status, string(resp.Body))
	}
	secretsMustNotLeak(t, resp.Body)

	h.StartRedis()
	h.WaitLimiterHealthy(60 * time.Second)
	// Sidecar may need a moment after Redis returns.
	deadline := time.Now().Add(60 * time.Second)
	var last Response
	for time.Now().Before(deadline) {
		last, err = reqOK(uniqueUser("sidecar-recover"))
		if err == nil && last.Status == http.StatusOK {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("sidecar recovery: want 200, last status=%d body=%s err=%v", last.Status, string(last.Body), err)
}
