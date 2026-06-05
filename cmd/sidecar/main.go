package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type CacheEntry struct {
	Allowed   bool
	Remaining int
	ExpiresAt time.Time
}

type Sidecar struct {
	upstreamURL string
	limiterURL  string
	cache       sync.Map
	ttl         time.Duration
	failOpen    bool
	limit       int
	httpClient  *http.Client
}

func NewSidecar(upstream, limiter string, ttl time.Duration, failOpen bool, limit int) *Sidecar {
	return &Sidecar{
		upstreamURL: upstream,
		limiterURL:  limiter,
		ttl:         ttl,
		failOpen:    failOpen,
		limit:       limit,
		httpClient:  &http.Client{Timeout: 5 * time.Second},
	}
}

func (s *Sidecar) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		userID = r.URL.Query().Get("user_id")
	}
	if userID == "" {
		userID = "anonymous"
	}

	log.Printf("[DEBUG] Request for user: %s", userID)

	if val, ok := s.cache.Load(userID); ok {
		entry := val.(CacheEntry)
		log.Printf("[DEBUG] Cache entry found – allowed=%v, remaining=%d, expires=%v", entry.Allowed, entry.Remaining, entry.ExpiresAt)
		if time.Now().Before(entry.ExpiresAt) {
			if !entry.Allowed {
				log.Printf("[DEBUG] Serving cached DENIAL for %s", userID)
				metrics.RecordCacheHit()
				w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", s.limit))
				w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", entry.Remaining))
				http.Error(w, "Too many requests", http.StatusTooManyRequests)
				return
			}
			log.Printf("[DEBUG] Cache entry is ALLOWED – ignoring cache, will call central limiter")
		} else {
			log.Printf("[DEBUG] Cache entry expired, deleting")
			s.cache.Delete(userID)
		}
	} else {
		log.Printf("[DEBUG] Cache miss for user %s", userID)
	}

	metrics.RecordCacheMiss()
	log.Printf("[DEBUG] Calling central limiter for %s", userID)
	allowed, remaining, err := s.checkRateLimit(userID)
	log.Printf("[DEBUG] checkRateLimit returned allowed=%v, remaining=%d, err=%v", allowed, remaining, err)
	if err != nil {
		log.Printf("Rate limiter error: %v", err)
		if s.failOpen {
			s.forwardRequest(w, r)
			return
		}
		http.Error(w, "Rate limiter unavailable", http.StatusServiceUnavailable)
		return
	}

	// Store the result in cache (both allowed and denied)
	s.cache.Store(userID, CacheEntry{
		Allowed:   allowed,
		Remaining: remaining,
		ExpiresAt: time.Now().Add(s.ttl),
	})

	if !allowed {
		w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", s.limit))
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		http.Error(w, "Too many requests", http.StatusTooManyRequests)
		return
	}

	// Allowed – forward to upstream
	w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", s.limit))
	w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
	s.forwardRequest(w, r)
}

func (s *Sidecar) checkRateLimit(userID string) (bool, int, error) {
	resp, err := s.httpClient.Get(fmt.Sprintf("%s/check?user_id=%s", s.limiterURL, userID))
	if err != nil {
		return false, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		remaining := 0
		if val := resp.Header.Get("X-RateLimit-Remaining"); val != "" {
			fmt.Sscanf(val, "%d", &remaining)
		}
		return false, remaining, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, 0, fmt.Errorf("limiter returned %d", resp.StatusCode)
	}

	remaining := 0
	if val := resp.Header.Get("X-RateLimit-Remaining"); val != "" {
		fmt.Sscanf(val, "%d", &remaining)
	}

	var result struct {
		Allowed bool `json:"allowed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, 0, err
	}
	return result.Allowed, remaining, nil
}

func (s *Sidecar) forwardRequest(w http.ResponseWriter, r *http.Request) {
	target, _ := url.Parse(s.upstreamURL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	r.Host = target.Host
	proxy.ServeHTTP(w, r)
}

func main() {
	upstream := os.Getenv("UPSTREAM_URL")
	limiter := os.Getenv("RATE_LIMITER_URL")
	if upstream == "" || limiter == "" {
		log.Fatal("UPSTREAM_URL and RATE_LIMITER_URL must be set")
	}

	ttl := 30 * time.Millisecond
	failOpen := os.Getenv("FAIL_OPEN") == "true"
	limit := 10
	if l := os.Getenv("RATE_LIMIT"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}

	sidecar := NewSidecar(upstream, limiter, ttl, failOpen, limit)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Simplified health check – just try calling limiter's health
		resp, err := sidecar.httpClient.Get(limiter + "/health")
		if err != nil || resp.StatusCode != http.StatusOK {
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

	log.Printf("Sidecar starting on :%s, forwarding to %s", port, upstream)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
