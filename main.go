package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/limiter"
    "github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/metrics"
    redisclient "github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/redis"
    "github.com/prometheus/client_golang/prometheus/promhttp"
)

var limiterInstance RateLimiter

func main() {
    cfg := LoadConfig()

    // Connect to Redis
    rdb := redisclient.NewClient(cfg.RedisAddr)
    log.Printf("Connected to Redis at %s", cfg.RedisAddr)

    // Use atomic Redis-backed token bucket with Lua script (NO race condition)
    limiterInstance = limiter.NewRedisAtomicTokenBucket(rdb, cfg.Capacity, cfg.RefillRate)
    log.Println("Using Redis-backed token bucket (ATOMIC with Lua)")

    mux := http.NewServeMux()

    // /metrics endpoint for Prometheus
    mux.Handle("/metrics", promhttp.Handler())

    // /health endpoint – checks Redis connectivity
    mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        if err := rdb.Ping(context.Background()).Err(); err != nil {
            w.WriteHeader(http.StatusServiceUnavailable)
            json.NewEncoder(w).Encode(map[string]string{
                "status": "unhealthy",
                "reason": "Redis unreachable",
            })
            return
        }
        w.WriteHeader(http.StatusOK)
        json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
    })

    // /check endpoint – rate limiting logic with metrics
    mux.HandleFunc("/check", func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        userID := r.URL.Query().Get("user_id")
        if userID == "" {
            userID = "anonymous"
        }

        allowed, remaining, err := limiterInstance.Allow(userID)

        if err != nil {
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusServiceUnavailable)
            json.NewEncoder(w).Encode(map[string]string{
                "error": "Rate limiter unavailable",
            })
            return
        }

        // Record Prometheus metrics
        metrics.RecordRequest(userID, allowed)
        metrics.RecordRequestDuration(userID, time.Since(start).Seconds())

        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", cfg.Capacity))
        w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))

        if !allowed {
            w.Header().Set("Retry-After", "60")
            w.WriteHeader(http.StatusTooManyRequests)
            json.NewEncoder(w).Encode(map[string]string{"error": "Too many requests"})
            return
        }
        json.NewEncoder(w).Encode(map[string]interface{}{
            "allowed":   true,
            "remaining": remaining,
        })
    })

    srv := &http.Server{
        Addr:         fmt.Sprintf(":%d", cfg.Port),
        Handler:      mux,
        ReadTimeout:  5 * time.Second,
        WriteTimeout: 10 * time.Second,
        IdleTimeout:  120 * time.Second,
    }

    // Start server in a goroutine
    go func() {
        log.Printf("Server starting on :%d", cfg.Port)
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatalf("listen: %s\n", err)
        }
    }()

    // Wait for interrupt signal
    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit
    log.Println("Shutting down server...")

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := srv.Shutdown(ctx); err != nil {
        log.Fatal("Server forced to shutdown:", err)
    }
    log.Println("Server exited")
}