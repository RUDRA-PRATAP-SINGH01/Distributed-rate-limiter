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
)

var limiter RateLimiter

func main() {
    cfg := LoadConfig()

    // Initialize limiter based on config
    if cfg.Algorithm == "sliding" {
        limiter = NewSlidingWindow(cfg.Capacity, time.Duration(cfg.WindowSec)*time.Second)
        log.Println("Using sliding window algorithm")
    } else {
        limiter = NewTokenBucket(cfg.Capacity, cfg.RefillRate)
        log.Println("Using token bucket algorithm")
    }

    mux := http.NewServeMux()
    mux.HandleFunc("/check", func(w http.ResponseWriter, r *http.Request) {
        userID := r.URL.Query().Get("user_id")
        if userID == "" {
            userID = "anonymous"
        }

        allowed, remaining := limiter.Allow(userID)

        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", cfg.Capacity))
        w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))

        if !allowed {
            w.Header().Set("Retry-After", "60")
            w.WriteHeader(http.StatusTooManyRequests)
            json.NewEncoder(w).Encode(map[string]string{"error": "Too many requests"})
            return
        }
        json.NewEncoder(w).Encode(map[string]bool{"allowed": true})
    })

    srv := &http.Server{
        Addr:    fmt.Sprintf(":%d", cfg.Port),
        Handler: mux,
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