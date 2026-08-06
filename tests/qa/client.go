// Package qa is the shared HTTP client for live-stack smoke and sanity tests.
// It talks to limiter/sidecar the same way production clients do: trusted
// identity headers and the internal API key — never ?user_id=.
package qa

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	DefaultLimiterURL = "http://127.0.0.1:8080"
	DefaultSidecarURL = "http://127.0.0.1:9090"
	DefaultAPIKey     = "dev-internal-key-change-in-prod"
	DefaultAdminURL   = "http://127.0.0.1:8082"
	DefaultAdminKey   = "dev-key-change-in-prod"
)

// Client is a black-box HTTP client for the compose stack.
type Client struct {
	LimiterURL string
	SidecarURL string
	AdminURL   string
	APIKey     string
	AdminKey   string
	HTTP       *http.Client
}

// FromEnv builds a client from QA_* / INTERNAL_API_KEY / ADMIN_API_KEY env vars.
func FromEnv() *Client {
	return &Client{
		LimiterURL: envOr("QA_LIMITER_URL", envOr("CHAOS_LIMITER_URL", DefaultLimiterURL)),
		SidecarURL: envOr("QA_SIDECAR_URL", envOr("CHAOS_SIDECAR_URL", DefaultSidecarURL)),
		AdminURL:   envOr("QA_ADMIN_URL", DefaultAdminURL),
		APIKey:     envOr("INTERNAL_API_KEY", DefaultAPIKey),
		AdminKey:   envOr("ADMIN_API_KEY", DefaultAdminKey),
		HTTP:       &http.Client{Timeout: 3 * time.Second},
	}
}

// Response is a trimmed HTTP result.
type Response struct {
	Status int
	Body   []byte
	Header http.Header
}

// StackReachable is true when limiter /health answers at all (any HTTP status).
func (c *Client) StackReachable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	resp, err := c.HealthLimiter(ctx)
	return err == nil && resp.Status > 0
}

// RequireStack is set when scripts want a hard fail instead of t.Skip.
func RequireStack() bool {
	return os.Getenv("QA_REQUIRE_STACK") == "1"
}

// HealthLimiter calls GET /health on the limiter.
func (c *Client) HealthLimiter(ctx context.Context) (Response, error) {
	return c.get(ctx, c.LimiterURL+"/health", nil)
}

// HealthSidecar calls GET /health on the sidecar.
func (c *Client) HealthSidecar(ctx context.Context) (Response, error) {
	return c.get(ctx, c.SidecarURL+"/health", nil)
}

// Check calls GET /check with production identity headers.
func (c *Client) Check(ctx context.Context, userID string) (Response, error) {
	return c.get(ctx, c.LimiterURL+"/check", map[string]string{
		"X-User-ID":          userID,
		"X-API-Key":          c.APIKey,
		"X-Internal-API-Key": c.APIKey,
	})
}

// CheckNoAuth calls GET /check with a user but no API key.
func (c *Client) CheckNoAuth(ctx context.Context, userID string) (Response, error) {
	return c.get(ctx, c.LimiterURL+"/check", map[string]string{
		"X-User-ID": userID,
	})
}

// SidecarProxy calls GET / on the sidecar (ALLOWED_PATHS=/ in compose).
func (c *Client) SidecarProxy(ctx context.Context, userID string) (Response, error) {
	return c.get(ctx, c.SidecarURL+"/", map[string]string{
		"X-User-ID": userID,
	})
}

// AdminUnauth calls GET /admin/limits/user/:id with no key.
func (c *Client) AdminUnauth(ctx context.Context) (Response, error) {
	return c.get(ctx, strings.TrimRight(c.AdminURL, "/")+"/admin/limits/user/sanity-probe", nil)
}

// UniqueUser returns a collision-resistant identity for a live Redis.
func UniqueUser(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func (c *Client) get(ctx context.Context, rawURL string, headers map[string]string) (Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Response{}, err
	}
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Response{}, err
	}
	return Response{Status: resp.StatusCode, Body: body, Header: resp.Header.Clone()}, nil
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
