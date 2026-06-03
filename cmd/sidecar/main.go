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
)

type CacheEntry struct {
    Allowed   bool
    Remaining int
    ExpiresAt time.Time
}

type Sidecar struct {
    upstreamURL   string
    limiterURL    string
    cache         sync.Map
    ttl           time.Duration
    failOpen      bool
}

func NewSidecar(upstream, limiter string, ttl time.Duration, failOpen bool) *Sidecar {
    return &Sidecar{
        upstreamURL: upstream,
        limiterURL:  limiter,
        ttl:         ttl,
        failOpen:    failOpen,
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

    // Only cache denials — caching "allowed" skips token consumption on later requests.
    if val, ok := s.cache.Load(userID); ok {
        entry := val.(CacheEntry)
        if time.Now().Before(entry.ExpiresAt) && !entry.Allowed {
            w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", entry.Remaining))
            http.Error(w, "Too many requests", http.StatusTooManyRequests)
            return
        }
        if time.Now().After(entry.ExpiresAt) {
            s.cache.Delete(userID)
        }
    }

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

    if !allowed {
        s.cache.Store(userID, CacheEntry{
            Allowed:   false,
            Remaining: remaining,
            ExpiresAt: time.Now().Add(s.ttl),
        })
        w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
        http.Error(w, "Too many requests", http.StatusTooManyRequests)
        return
    }
    s.forwardRequest(w, r)
}

func (s *Sidecar) checkRateLimit(userID string) (bool, int, error) {
    resp, err := http.Get(fmt.Sprintf("%s/check?user_id=%s", s.limiterURL, userID))
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

    sidecar := NewSidecar(upstream, limiter, ttl, failOpen)
    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
    }

    log.Printf("Sidecar starting on :%s, forwarding to %s", port, upstream)
    log.Fatal(http.ListenAndServe(":"+port, sidecar))
}