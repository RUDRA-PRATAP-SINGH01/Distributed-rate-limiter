package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	redisclient "github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/redis"
	"github.com/redis/go-redis/v9"
)

// sidecarHealthDeps configures readiness evaluation for /health.
type sidecarHealthDeps struct {
	needsRedis  bool
	limiterURL  string
	httpClient  *http.Client
	redisClient redis.UniversalClient
	redisCfg    redisclient.Config
}

func newSidecarHealthHandler(deps sidecarHealthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code, body := evaluateSidecarHealth(r.Context(), deps)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(body)
	}
}

func evaluateSidecarHealth(ctx context.Context, deps sidecarHealthDeps) (int, map[string]interface{}) {
	limiterOK := checkLimiterHealth(ctx, deps.httpClient, deps.limiterURL)
	if !limiterOK {
		return http.StatusServiceUnavailable, map[string]interface{}{"status": "unhealthy"}
	}

	if deps.needsRedis {
		if deps.redisClient == nil {
			return http.StatusServiceUnavailable, map[string]interface{}{
				"status": "unhealthy",
				"redis":  redisclient.Health{Mode: deps.redisCfg.Mode, Error: "redis client not configured"},
			}
		}
		h := redisclient.CheckHealth(ctx, deps.redisClient, deps.redisCfg)
		if !h.Connected {
			return http.StatusServiceUnavailable, map[string]interface{}{
				"status": "unhealthy",
				"redis":  h,
			}
		}
		return http.StatusOK, map[string]interface{}{
			"status": "healthy",
			"redis":  h,
		}
	}

	return http.StatusOK, map[string]interface{}{"status": "healthy"}
}

func checkLimiterHealth(ctx context.Context, client *http.Client, limiterURL string) bool {
	if client == nil || limiterURL == "" {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, limiterURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}
