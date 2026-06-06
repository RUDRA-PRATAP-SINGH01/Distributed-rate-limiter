package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/auth"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/identity"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/singleflight"
)

type CacheEntry struct {
	Allowed    bool
	Remaining  int
	Limit      int
	RetryAfter string
	ExpiresAt  time.Time
}

type limitResult struct {
	allowed    bool
	remaining  int
	limit      int
	retryAfter string
}

type Sidecar struct {
	upstreamURL      string
	limiterURL       string
	internalAPIKey   string
	metricsAPIKey    string
	allowQueryUserID bool
	allowedPaths     []string
	debug            bool
	cache            sync.Map
	limitFlight      singleflight.Group
	ttl              time.Duration
	failOpen         bool
	defaultLimit     int
	useHierarchical  bool
	httpClient       *http.Client
	proxy            *httputil.ReverseProxy
}

func NewSidecar(
	upstream, limiter, internalAPIKey, metricsAPIKey string,
	ttl time.Duration,
	failOpen bool,
	defaultLimit int,
	useHierarchical, allowQueryUserID, debug bool,
	allowedPaths []string,
) *Sidecar {
	target, err := url.Parse(upstream)
	if err != nil {
		log.Fatalf("invalid UPSTREAM_URL: %v", err)
	}
	return &Sidecar{
		upstreamURL:      upstream,
		limiterURL:       limiter,
		internalAPIKey:   internalAPIKey,
		metricsAPIKey:    metricsAPIKey,
		allowQueryUserID: allowQueryUserID,
		allowedPaths:     allowedPaths,
		debug:            debug,
		ttl:              ttl,
		failOpen:         failOpen,
		defaultLimit:     defaultLimit,
		useHierarchical:  useHierarchical,
		httpClient:       &http.Client{Timeout: 5 * time.Second},
		proxy:            httputil.NewSingleHostReverseProxy(target),
	}
}

func (s *Sidecar) debugf(format string, args ...interface{}) {
	if s.debug {
		log.Printf("[DEBUG] "+format, args...)
	}
}

func (s *Sidecar) cacheKey(r *http.Request, userID string) string {
	if !s.useHierarchical {
		return userID
	}
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = r.URL.Query().Get("tenant_id")
	}
	if tenantID == "" {
		tenantID = "default"
	}
	return tenantID + "|" + userID + "|" + r.URL.Path
}

func (s *Sidecar) pathAllowed(path string) bool {
	if len(s.allowedPaths) == 0 {
		return true
	}
	for _, prefix := range s.allowedPaths {
		if path == prefix || strings.HasPrefix(path, prefix+"/") || strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func (s *Sidecar) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health" || r.URL.Path == "/metrics" {
		http.NotFound(w, r)
		return
	}

	if !s.pathAllowed(r.URL.Path) {
		http.Error(w, "path not allowed", http.StatusNotFound)
		return
	}

	userID, err := identity.ResolveUserID(r, s.allowQueryUserID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cacheKey := s.cacheKey(r, userID)
	s.debugf("Request for user %s (cache key: %s)", userID, cacheKey)

	if val, ok := s.cache.Load(cacheKey); ok {
		entry := val.(CacheEntry)
		if time.Now().Before(entry.ExpiresAt) {
			if !entry.Allowed {
				s.debugf("Serving cached DENIAL for %s", cacheKey)
				metrics.RecordCacheHit()
				s.writeDenial(w, entry.Limit, entry.Remaining, entry.RetryAfter)
				return
			}
			s.debugf("Cache entry is ALLOWED – ignoring cache, will call central limiter")
		} else {
			s.cache.Delete(cacheKey)
		}
	} else {
		s.debugf("Cache miss for cache key %s", cacheKey)
	}

	metrics.RecordCacheMiss()

	resultAny, err, _ := s.limitFlight.Do(cacheKey, func() (interface{}, error) {
		return s.checkRateLimit(r, userID)
	})
	if err != nil {
		log.Printf("Rate limiter error: %v", err)
		if s.failOpen {
			log.Printf("WARNING: FAIL_OPEN enabled — forwarding request despite limiter error")
			s.forwardRequest(w, r)
			return
		}
		http.Error(w, "Rate limiter unavailable", http.StatusServiceUnavailable)
		return
	}

	result := resultAny.(limitResult)
	s.cache.Store(cacheKey, CacheEntry{
		Allowed:    result.allowed,
		Remaining:  result.remaining,
		Limit:      result.limit,
		RetryAfter: result.retryAfter,
		ExpiresAt:  time.Now().Add(s.ttl),
	})

	if !result.allowed {
		s.writeDenial(w, result.limit, result.remaining, result.retryAfter)
		return
	}

	s.writeRateLimitHeaders(w, result.limit, result.remaining)
	s.forwardRequest(w, r)
}

func (s *Sidecar) writeRateLimitHeaders(w http.ResponseWriter, limit, remaining int) {
	w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
	w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
}

func (s *Sidecar) writeDenial(w http.ResponseWriter, limit, remaining int, retryAfter string) {
	s.writeRateLimitHeaders(w, limit, remaining)
	if retryAfter != "" {
		w.Header().Set("Retry-After", retryAfter)
	}
	http.Error(w, "Too many requests", http.StatusTooManyRequests)
}

func (s *Sidecar) checkRateLimit(r *http.Request, userID string) (limitResult, error) {
	var req *http.Request
	var err error

	if s.useHierarchical {
		endpoint := r.URL.Path
		if endpoint == "" {
			endpoint = "/"
		}
		checkURL := fmt.Sprintf(
			"%s/check_hierarchical?endpoint=%s",
			s.limiterURL,
			url.QueryEscape(endpoint),
		)
		req, err = http.NewRequestWithContext(r.Context(), http.MethodGet, checkURL, nil)
	} else {
		checkURL := fmt.Sprintf("%s/check", s.limiterURL)
		req, err = http.NewRequestWithContext(r.Context(), http.MethodGet, checkURL, nil)
	}
	if err != nil {
		return limitResult{}, err
	}

	req.Header.Set(identity.UserIDHeader, userID)
	if s.internalAPIKey != "" {
		req.Header.Set(auth.InternalAPIKeyHeader, s.internalAPIKey)
	}
	if tenantID := r.Header.Get("X-Tenant-ID"); tenantID != "" {
		req.Header.Set("X-Tenant-ID", tenantID)
	} else if tenantID := r.URL.Query().Get("tenant_id"); tenantID != "" {
		req.Header.Set("X-Tenant-ID", tenantID)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return limitResult{}, err
	}
	defer resp.Body.Close()

	limit := s.defaultLimit
	if val := resp.Header.Get("X-RateLimit-Limit"); val != "" {
		fmt.Sscanf(val, "%d", &limit)
	}
	remaining := 0
	if val := resp.Header.Get("X-RateLimit-Remaining"); val != "" {
		fmt.Sscanf(val, "%d", &remaining)
	}
	retryAfter := resp.Header.Get("Retry-After")

	if resp.StatusCode == http.StatusTooManyRequests {
		return limitResult{allowed: false, remaining: remaining, limit: limit, retryAfter: retryAfter}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return limitResult{}, fmt.Errorf("limiter returned %d", resp.StatusCode)
	}

	var body struct {
		Allowed bool `json:"allowed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return limitResult{}, err
	}
	return limitResult{allowed: body.Allowed, remaining: remaining, limit: limit, retryAfter: retryAfter}, nil
}

func (s *Sidecar) forwardRequest(w http.ResponseWriter, r *http.Request) {
	target, _ := url.Parse(s.upstreamURL)
	r.Host = target.Host
	s.proxy.ServeHTTP(w, r)
}

func parseAllowedPaths(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func sidecarMetricsAuthKey(internalKey, metricsKey string) string {
	if metricsKey != "" {
		return metricsKey
	}
	return internalKey
}

func main() {
	upstream := os.Getenv("UPSTREAM_URL")
	limiter := os.Getenv("RATE_LIMITER_URL")
	if upstream == "" || limiter == "" {
		log.Fatal("UPSTREAM_URL and RATE_LIMITER_URL must be set")
	}

	ttl := 30 * time.Millisecond
	if raw := os.Getenv("CACHE_TTL_MS"); raw != "" {
		if ms, err := strconv.Atoi(raw); err == nil && ms > 0 {
			ttl = time.Duration(ms) * time.Millisecond
		}
	}

	failOpen := os.Getenv("FAIL_OPEN") == "true"
	if failOpen {
		log.Printf("WARNING: FAIL_OPEN=true — sidecar forwards all traffic when limiter/Redis is down. Never use in production.")
	}

	useHierarchical := os.Getenv("USE_HIERARCHICAL") == "true"
	allowQueryUserID := os.Getenv("ALLOW_QUERY_USER_ID") == "true"
	debug := os.Getenv("DEBUG") == "true"
	internalAPIKey := os.Getenv("INTERNAL_API_KEY")
	metricsRequireAuth := os.Getenv("METRICS_REQUIRE_AUTH") == "true"
	metricsAPIKey := sidecarMetricsAuthKey(internalAPIKey, os.Getenv("METRICS_API_KEY"))
	metricsKey := ""
	if metricsRequireAuth {
		metricsKey = metricsAPIKey
	}
	allowedPaths := parseAllowedPaths(os.Getenv("ALLOWED_PATHS"))

	if len(allowedPaths) == 0 {
		log.Printf("WARNING: ALLOWED_PATHS is not set — all paths are proxied. Set ALLOWED_PATHS=/ for production hardening.")
	}

	defaultLimit := 10
	if l := os.Getenv("RATE_LIMIT"); l != "" {
		fmt.Sscanf(l, "%d", &defaultLimit)
	}

	tlsCert := os.Getenv("TLS_CERT_FILE")
	tlsKey := os.Getenv("TLS_KEY_FILE")
	if (tlsCert != "" || tlsKey != "") && (tlsCert == "" || tlsKey == "") {
		log.Fatal("TLS_CERT_FILE and TLS_KEY_FILE must both be set to enable TLS")
	}

	sidecar := NewSidecar(
		upstream, limiter, internalAPIKey, metricsAPIKey,
		ttl, failOpen, defaultLimit, useHierarchical, allowQueryUserID, debug, allowedPaths,
	)

	mux := http.NewServeMux()
	mux.Handle("/metrics", auth.RequireAPIKey(metricsKey, promhttp.Handler().ServeHTTP))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp, err := sidecar.httpClient.Get(limiter + "/health")
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"status": "unhealthy"})
			return
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)

		if resp.StatusCode != http.StatusOK {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"status": "unhealthy"})
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
	})
	mux.Handle("/", sidecar)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mode := "simple /check"
	if useHierarchical {
		mode = "hierarchical /check_hierarchical"
	}
	log.Printf("Sidecar starting on :%s, forwarding to %s, limiter mode: %s", port, upstream, mode)

	if tlsCert != "" {
		log.Fatal(http.ListenAndServeTLS(":"+port, tlsCert, tlsKey, mux))
	}
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
