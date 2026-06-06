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
var hierarchicalLimiter *limiter.HierarchicalLimiter

func main() {
	cfg := LoadConfig()

	rdb := redisclient.NewClient(cfg.RedisAddr)
	log.Printf("Connected to Redis at %s", cfg.RedisAddr)

	switch cfg.Algorithm {
	case "sliding":
		limiterInstance = limiter.NewRedisSlidingWindow(rdb, cfg.Capacity, time.Duration(cfg.WindowSec)*time.Second)
		log.Printf("Using Redis sliding window algorithm (limit=%d, window=%ds)", cfg.Capacity, cfg.WindowSec)
	case "token":
		fallthrough
	default:
		limiterInstance = limiter.NewRedisAtomicTokenBucket(rdb, cfg.Capacity, cfg.RefillRate)
		log.Println("Using Redis token bucket (atomic Lua)")
	}

	if cfg.EnableHierarchical {
		hierarchicalLimiter = limiter.NewHierarchicalLimiter(
			rdb,
			cfg.GlobalCapacity, cfg.TenantCapacity, cfg.UserCapacity, cfg.EndpointCapacity,
			cfg.GlobalRefillRate, cfg.TenantRefillRate, cfg.UserRefillRate, cfg.EndpointRefillRate,
		)
		log.Println("Hierarchical limiter enabled (global / tenant / user / endpoint)")
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

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

		metrics.RecordRequest(userID, allowed)
		metrics.RecordRequestDuration(userID, time.Since(start).Seconds())

		w.Header().Set("Content-Type", "application/json")
		setRateLimitHeaders(w, rateLimitLimitHeader(cfg), remaining)

		if !allowed {
			w.Header().Set("Retry-After", retryAfterForCheck(cfg))
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]string{"error": "Too many requests"})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"allowed":   true,
			"remaining": remaining,
		})
	})

	if cfg.EnableHierarchical {
		mux.HandleFunc("/check_hierarchical", func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			userID := r.URL.Query().Get("user_id")
			if userID == "" {
				userID = "anonymous"
			}
			tenantID := r.Header.Get("X-Tenant-ID")
			if tenantID == "" {
				tenantID = r.URL.Query().Get("tenant_id")
			}
			if tenantID == "" {
				tenantID = "default"
			}
			endpoint := r.URL.Query().Get("endpoint")
			if endpoint == "" {
				endpoint = "default"
			}

			globalKey := "rate:global"
			tenantKey := fmt.Sprintf("rate:tenant:%s", tenantID)
			userKey := fmt.Sprintf("rate:user:%s", userID)
			endpointKey := fmt.Sprintf("rate:endpoint:%s:%s", tenantID, endpoint)

			allowed, remaining, err := hierarchicalLimiter.Allow(globalKey, tenantKey, userKey, endpointKey)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				json.NewEncoder(w).Encode(map[string]string{
					"error": "Hierarchical rate limiter unavailable",
				})
				return
			}

			metrics.RecordRequest(userID, allowed)
			metrics.RecordRequestDuration(userID, time.Since(start).Seconds())

			w.Header().Set("Content-Type", "application/json")
			setRateLimitHeaders(w, hierarchicalRateLimitLimitHeader(cfg), remaining)

			if !allowed {
				w.Header().Set("Retry-After", retryAfterForHierarchical(cfg))
				w.WriteHeader(http.StatusTooManyRequests)
				json.NewEncoder(w).Encode(map[string]string{"error": "Too many requests (hierarchical limit)"})
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"allowed":   true,
				"remaining": remaining,
				"message":   "Request allowed by all levels",
			})
		})
	}

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("Server starting on :%d", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()

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
