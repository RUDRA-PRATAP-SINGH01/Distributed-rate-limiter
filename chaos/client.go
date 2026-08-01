//go:build chaos

package chaos

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client is a production-shaped HTTP client for resilience contracts.
// It always sends trusted identity headers and the internal API key — never
// ?user_id= — so contracts keep working after auth hardening.
type Client struct {
	BaseURL        string
	InternalAPIKey string
	HTTP           *http.Client
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL:        strings.TrimRight(baseURL, "/"),
		InternalAPIKey: apiKey,
		HTTP: &http.Client{
			Timeout: 3 * time.Second,
		},
	}
}

func ClientFromEnv(defaultBase string) *Client {
	base := envOr("CHAOS_LIMITER_URL", defaultBase)
	key := envOr("INTERNAL_API_KEY", "dev-internal-key-change-in-prod")
	return NewClient(base, key)
}

type Response struct {
	Status int
	Body   []byte
	Header http.Header
}

// Check calls GET /check with X-User-ID and X-Internal-API-Key.
func (c *Client) Check(ctx context.Context, userID string) (Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/check", nil)
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("X-User-ID", userID)
	if c.InternalAPIKey != "" {
		req.Header.Set("X-Internal-API-Key", c.InternalAPIKey)
		req.Header.Set("X-API-Key", c.InternalAPIKey)
	}
	return c.do(req)
}

// Health calls GET /health.
func (c *Client) Health(ctx context.Context) (Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/health", nil)
	if err != nil {
		return Response{}, err
	}
	return c.do(req)
}

func (c *Client) do(req *http.Request) (Response, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
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

func uniqueUser(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}
