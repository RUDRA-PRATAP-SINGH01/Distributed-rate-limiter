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
	"sync"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type CacheEntry struct {
	Allowed   bool
	Remaining int
	Limit     int
	ExpiresAt time.Time
}

type Sidecar struct {
	upstreamURL     string
	limiterURL      string
	cache           sync.Map
	ttl             time.Duration
	failOpen        bool
	defaultLimit    int
	useHierarchical bool
	httpClient      *http.Client
	proxy           *httputil.ReverseProxy
}

func NewSidecar(upstream, limiter string, ttl time.Duration, failOpen bool, defaultLimit int, useHierarchical bool) *Sidecar {
	target, err := url.Parse(upstream)
	if err != nil {
		log.Fatalf("invalid UPSTREAM_URL: %v", err)
	}
	return &Sidecar{
		upstreamURL:     upstream,
		limiterURL:      limiter,
		ttl:             ttl,
		failOpen:        failOpen,
		defaultLimit:    defaultLimit,
		useHierarchical: useHierarchical,
		httpClient:      &http.Client{Timeout: 5 * time.Second},
		proxy:           httputil.NewSingleHostReverseProxy(target),
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

func (s *Sidecar) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		userID = r.URL.Query().Get("user_id")
	}
	if userID == "" {
		userID = "anonymous"
	}

	cacheKey := s.cacheKey(r, userID)
	log.Printf("[DEBUG] Request for user: %s (cache key: %s)", userID, cacheKey)

	if val, ok := s.cache.Load(cacheKey); ok {
		entry := val.(CacheEntry)
		log.Printf("[DEBUG] Cache entry found – allowed=%v, remaining=%d, expires=%v", entry.Allowed, entry.Remaining, entry.ExpiresAt)
		if time.Now().Before(entry.ExpiresAt) {
			if !entry.Allowed {
				log.Printf("[DEBUG] Serving cached DENIAL for %s", cacheKey)
				metrics.RecordCacheHit()
				s.writeRateLimitHeaders(w, entry.Limit, entry.Remaining)
				http.Error(w, "Too many requests", http.StatusTooManyRequests)
				return
			}
			log.Printf("[DEBUG] Cache entry is ALLOWED – ignoring cache, will call central limiter")
		} else {
			log.Printf("[DEBUG] Cache entry expired, deleting")
			s.cache.Delete(cacheKey)
		}
	} else {
		log.Printf("[DEBUG] Cache miss for cache key %s", cacheKey)
	}

	metrics.RecordCacheMiss()
	log.Printf("[DEBUG] Calling central limiter for %s", userID)
	allowed, remaining, limit, err := s.checkRateLimit(r, userID)
	log.Printf("[DEBUG] checkRateLimit returned allowed=%v, remaining=%d, limit=%d, err=%v", allowed, remaining, limit, err)
	if err != nil {
		log.Printf("Rate limiter error: %v", err)
		if s.failOpen {
			s.forwardRequest(w, r)
			return
		}
		http.Error(w, "Rate limiter unavailable", http.StatusServiceUnavailable)
		return
	}

	s.cache.Store(cacheKey, CacheEntry{
		Allowed:   allowed,
		Remaining: remaining,
		Limit:     limit,
		ExpiresAt: time.Now().Add(s.ttl),
	})

	if !allowed {
		s.writeRateLimitHeaders(w, limit, remaining)
		http.Error(w, "Too many requests", http.StatusTooManyRequests)
		return
	}

	s.writeRateLimitHeaders(w, limit, remaining)
	s.forwardRequest(w, r)
}

func (s *Sidecar) writeRateLimitHeaders(w http.ResponseWriter, limit, remaining int) {
	w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
	w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
}

func (s *Sidecar) checkRateLimit(r *http.Request, userID string) (bool, int, int, error) {
	var req *http.Request
	var err error

	if s.useHierarchical {
		endpoint := r.URL.Path
		if endpoint == "" {
			endpoint = "/"
		}
		checkURL := fmt.Sprintf(
			"%s/check_hierarchical?user_id=%s&endpoint=%s",
			s.limiterURL,
			url.QueryEscape(userID),
			url.QueryEscape(endpoint),
		)
		req, err = http.NewRequestWithContext(r.Context(), http.MethodGet, checkURL, nil)
		if err != nil {
			return false, 0, s.defaultLimit, err
		}
		if tenantID := r.Header.Get("X-Tenant-ID"); tenantID != "" {
			req.Header.Set("X-Tenant-ID", tenantID)
		} else if tenantID := r.URL.Query().Get("tenant_id"); tenantID != "" {
			req.Header.Set("X-Tenant-ID", tenantID)
		}
	} else {
		checkURL := fmt.Sprintf("%s/check?user_id=%s", s.limiterURL, url.QueryEscape(userID))
		req, err = http.NewRequestWithContext(r.Context(), http.MethodGet, checkURL, nil)
		if err != nil {
			return false, 0, s.defaultLimit, err
		}
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return false, 0, s.defaultLimit, err
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

	if resp.StatusCode == http.StatusTooManyRequests {
		return false, remaining, limit, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, 0, limit, fmt.Errorf("limiter returned %d", resp.StatusCode)
	}

	var result struct {
		Allowed bool `json:"allowed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, 0, limit, err
	}
	return result.Allowed, remaining, limit, nil
}

func (s *Sidecar) forwardRequest(w http.ResponseWriter, r *http.Request) {
	target, _ := url.Parse(s.upstreamURL)
	r.Host = target.Host
	s.proxy.ServeHTTP(w, r)
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
	useHierarchical := os.Getenv("USE_HIERARCHICAL") == "true"

	defaultLimit := 10
	if l := os.Getenv("RATE_LIMIT"); l != "" {
		fmt.Sscanf(l, "%d", &defaultLimit)
	}

	sidecar := NewSidecar(upstream, limiter, ttl, failOpen, defaultLimit, useHierarchical)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
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
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
