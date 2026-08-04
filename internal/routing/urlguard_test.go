package routing

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateGatewayURL(t *testing.T) {
	t.Parallel()
	if err := ValidateGatewayURL("http://payments.example.com:8081", false); err != nil {
		t.Fatalf("public host: %v", err)
	}
	if err := ValidateGatewayURL("http://gateway-a:8081", false); err != nil {
		t.Fatalf("docker DNS name is syntactically ok: %v", err)
	}
	if err := ValidateGatewayURL("http://127.0.0.1:8082", false); err == nil {
		t.Fatal("loopback must be rejected without AllowPrivate")
	}
	if err := ValidateGatewayURL("http://127.0.0.1:8082", true); err != nil {
		t.Fatalf("loopback with AllowPrivate: %v", err)
	}
	if err := ValidateGatewayURL("http://169.254.169.254/latest/meta-data", true); err == nil {
		t.Fatal("IMDS must be rejected even with AllowPrivate")
	}
	if err := ValidateGatewayURL("http://10.0.0.5:80", false); err == nil {
		t.Fatal("RFC1918 must be rejected without AllowPrivate")
	}
	if err := ValidateGatewayURL("ftp://evil.example", false); err == nil {
		t.Fatal("non-http scheme must be rejected")
	}
	if err := ValidateGatewayURL("http://user:pass@evil.example", false); err == nil {
		t.Fatal("userinfo must be rejected")
	}
	if err := ValidateGatewayURL("http://metadata.google.internal/", true); err == nil {
		t.Fatal("metadata hostname must be blocked")
	}
}

func TestParseGatewaysEnvRejectsSSRF(t *testing.T) {
	t.Parallel()
	if _, err := ParseGatewaysEnv("gw|http://169.254.169.254/|100", true); err == nil {
		t.Fatal("expected IMDS GATEWAYS entry to fail")
	}
	gws, err := ParseGatewaysEnv("gateway-a|http://gateway-a:8081|100", false)
	if err != nil || len(gws) != 1 {
		t.Fatalf("docker hostname: %#v %v", gws, err)
	}
}

func TestGuardHTTPClientBlocksIMDS(t *testing.T) {
	t.Parallel()
	client := GuardHTTPClient(&http.Client{}, true)
	req, err := http.NewRequest(http.MethodGet, "http://169.254.169.254/", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(req)
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("expected blocked IMDS dial, got %v", err)
	}
}

func TestGuardHTTPClientAllowsLoopbackWhenOptedIn(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	denied := GuardHTTPClient(&http.Client{}, false)
	resp, err := denied.Get(upstream.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("httptest loopback must be denied without AllowPrivate")
	}

	allowed := GuardHTTPClient(&http.Client{}, true)
	resp, err = allowed.Get(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestGuardHTTPClientRevalidatesRedirect(t *testing.T) {
	t.Parallel()
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/", http.StatusFound)
	}))
	t.Cleanup(evil.Close)

	client := GuardHTTPClient(&http.Client{}, true)
	resp, err := client.Get(evil.URL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected redirect to IMDS to be rejected")
	}
}
