package main

import (
    "encoding/json"
    "fmt"
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

    // Check local cache – but only for denials (429)
    if val, ok := s.cache.Load(userID); ok {
        entry := val.(CacheEntry)
        if time.Now().Before(entry.ExpiresAt) {
            // Cache hit – only serve if it's a denial
            if !entry.Allowed {
                metrics.RecordCacheHit()
                w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", s.limit))
                w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", entry.Remaining))
                http.Error(w, "Too many requests", http.StatusTooManyRequests)
                return
            }
            // If allowed, we ignore cache and continue to central limiter
            // (do NOT return here)
        } else {
            s.cache.Delete(userID)
        }
    }

    // If we reach here, we must call central limiter (for allowed requests or expired)
    metrics.RecordCacheMiss()
    log.Printf("Cache miss or allowed – calling central limiter for user %s", userID)
    allowed, remaining, err := s.checkRateLimit(userID)
    if err != nil {
        log.Printf("Rate limiter error: %v", err)
        if s.failOpen {
            s.forwardRequest(w, r)
            return
        }
        http.Error(w, "Rate limiter unavailable", http.StatusServiceUnavailable)
        return
    }

    // Store in cache (only denials are served from cache; allowed entries are ignored on read)
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

func (s *Sidecar) healthCheck() error {
    resp, err := s.httpClient.Get(s.limiterURL + "/health")
    if err != nil {
        return fmt.Errorf("rate limiter unreachable: %w", err)
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("rate limiter unhealthy: status %d", resp.StatusCode)
    }
    return nil
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
    if val := os.Getenv("RATE_LIMIT"); val != "" {
        if parsed, err := strconv.Atoi(val); err == nil {
            limit = parsed
        }
    }

    sidecar := NewSidecar(upstream, limiter, ttl, failOpen, limit)

    mux := http.NewServeMux()
    mux.Handle("/metrics", promhttp.Handler())
    mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        if err := sidecar.healthCheck(); err != nil {
            w.WriteHeader(http.StatusServiceUnavailable)
            json.NewEncoder(w).Encode(map[string]string{
                "status": "unhealthy",
                "reason": err.Error(),
            })
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
