package main

import (
    "encoding/json"
    "log"
    "net/http"
    "os"
    "time"
)

var limiter RateLimiter

func main() {
    algo := os.Getenv("ALGORITHM")
    if algo == "sliding" {
        limiter = NewSlidingWindow(10, 60*time.Second)
        log.Println("Using sliding window algorithm")
    } else {
        limiter = NewTokenBucket(10, 1.0)
        log.Println("Using token bucket algorithm")
    }

    http.HandleFunc("/check", func(w http.ResponseWriter, r *http.Request) {
        userID := r.URL.Query().Get("user_id")
        if userID == "" {
            userID = "anonymous"
        }
        allowed := limiter.Allow(userID)
        w.Header().Set("Content-Type", "application/json")
        if !allowed {
            w.WriteHeader(http.StatusTooManyRequests)
            json.NewEncoder(w).Encode(map[string]string{"error": "Too many requests"})
            return
        }
        json.NewEncoder(w).Encode(map[string]bool{"allowed": true})
    })

    log.Println("Server starting on :8080")
    log.Fatal(http.ListenAndServe(":8080", nil))
}