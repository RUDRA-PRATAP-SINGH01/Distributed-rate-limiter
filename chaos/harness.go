//go:build chaos

package chaos

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Harness drives a minimal Compose stack for resilience contracts.
type Harness struct {
	t          *testing.T
	composeDir string
	composeFiles []string
	project    string
	LimiterURL string
	SidecarURL string
	startedByUs bool
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
}

// StartChaosStack brings up redis+limiter(+sidecar) via docker-compose.chaos.yml
// unless CHAOS_LIMITER_URL is already set (external stack).
func StartChaosStack(t *testing.T) *Harness {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}

	h := &Harness{
		t:          t,
		composeDir: repoRoot(t),
		composeFiles: []string{"docker-compose.chaos.yml"},
		project:    "rate-chaos",
		LimiterURL: envOr("CHAOS_LIMITER_URL", "http://127.0.0.1:8080"),
		SidecarURL: envOr("CHAOS_SIDECAR_URL", "http://127.0.0.1:9090"),
	}

	// If caller already pointed at a live stack, don't start Compose.
	if os.Getenv("CHAOS_EXTERNAL_STACK") == "true" {
		h.waitHealthy(t, 60*time.Second)
		return h
	}

	h.compose("up", "-d", "--build")
	h.startedByUs = true
	t.Cleanup(func() {
		if h.startedByUs {
			h.compose("down", "-v", "--remove-orphans")
		}
	})
	h.waitHealthy(t, 180*time.Second)
	return h
}

func (h *Harness) compose(args ...string) {
	h.t.Helper()
	cmdArgs := []string{"compose", "-p", h.project}
	for _, f := range h.composeFiles {
		cmdArgs = append(cmdArgs, "-f", f)
	}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command("docker", cmdArgs...)
	cmd.Dir = h.composeDir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		h.t.Fatalf("docker %v failed: %v\n%s", cmdArgs, err, buf.String())
	}
}

func (h *Harness) StopRedis() {
	h.t.Helper()
	h.compose("stop", "redis")
}

func (h *Harness) StartRedis() {
	h.t.Helper()
	h.compose("start", "redis")
}

func (h *Harness) waitHealthy(t *testing.T, timeout time.Duration) {
	t.Helper()
	client := NewClient(h.LimiterURL, envOr("INTERNAL_API_KEY", "dev-internal-key-change-in-prod"))
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		resp, err := client.Health(ctx)
		cancel()
		if err == nil && resp.Status == 200 {
			return
		}
		if err != nil {
			last = err
		} else {
			last = fmt.Errorf("health status %d: %s", resp.Status, string(resp.Body))
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("limiter not healthy within %s: %v", timeout, last)
}

func (h *Harness) WaitLimiterHealthy(timeout time.Duration) {
	h.waitHealthy(h.t, timeout)
}

func secretsMustNotLeak(t *testing.T, body []byte) {
	t.Helper()
	s := strings.ToLower(string(body))
	forbidden := []string{
		"dev-redis-password",
		"redis-password",
		"127.0.0.1:6379",
		"redis:6379",
	}
	for _, f := range forbidden {
		if strings.Contains(s, strings.ToLower(f)) {
			t.Fatalf("response leaked secret/backend detail %q: %s", f, string(body))
		}
	}
}
