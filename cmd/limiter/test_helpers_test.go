package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/audit"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/auth"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/circuitbreaker"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/identity"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/limiter"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/override"
	redisclient "github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/redis"
	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type testFixture struct {
	cfg      Config
	mr       *miniredis.Miniredis
	rdb      redis.UniversalClient
	mainSrv  *httptest.Server
	adminSrv *httptest.Server
	mainURL  string
	adminURL string
}

func newTestFixture(t *testing.T, overrideCfg func(*Config)) (*testFixture, func()) {
	t.Helper()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	cfg := Config{
		Port:               0,
		RedisAddr:          mr.Addr(),
		RedisPassword:      "",
		Algorithm:          "token",
		Capacity:           10,
		RefillRate:         1.0,
		WindowSec:          60,
		EnableHierarchical: true,
		EnableAdminAPI:     true,
		AdminPort:          0,
		AdminAPIKey:        "test-admin-key",
		OverrideCacheTTLMs: 1000,
		InternalAPIKey:     "test-internal-key",
		MetricsAPIKey:      "test-metrics-key",
		MetricsRequireAuth: true,
		AllowQueryUserID:   true,
		StrictConfig:       true,
		StrictSecurity:     false,
		GlobalCapacity:     1000,
		GlobalRefillRate:   100.0,
		TenantCapacity:     100,
		TenantRefillRate:   10.0,
		UserCapacity:       10,
		UserRefillRate:     1.0,
		EndpointCapacity:   5,
		EndpointRefillRate: 0.5,
	}

	if overrideCfg != nil {
		overrideCfg(&cfg)
	}

	// Assign package globals for main handler reference
	overrideStore = override.NewStore(rdb, time.Duration(cfg.OverrideCacheTTLMs)*time.Millisecond)

	cbCfg := circuitbreaker.DefaultConfig()
	cbCfg.MinSamples = 2
	cbCfg.ConsecutiveFailures = 2
	redisCircuit = circuitbreaker.NewBreaker(circuitbreaker.NewLocalStore(cbCfg))

	auditCfg := audit.DefaultConfig()
	auditStore = audit.NewStore(rdb, auditCfg)

	switch cfg.Algorithm {
	case "sliding":
		limiterInstance = limiter.NewRedisSlidingWindow(rdb, cfg.Capacity, time.Duration(cfg.WindowSec)*time.Second)
	default:
		limiterInstance = limiter.NewRedisAtomicTokenBucket(rdb, cfg.Capacity, cfg.RefillRate)
	}

	if cfg.EnableHierarchical {
		hierarchicalLimiter = limiter.NewHierarchicalLimiter(
			rdb,
			cfg.GlobalCapacity, cfg.TenantCapacity, cfg.UserCapacity, cfg.EndpointCapacity,
			cfg.GlobalRefillRate, cfg.TenantRefillRate, cfg.UserRefillRate, cfg.EndpointRefillRate,
		)
	}

	// Setup main mux
	mainMux := http.NewServeMux()
	metricsKey := ""
	if cfg.MetricsRequireAuth {
		metricsKey = cfg.MetricsAuthKey()
	}
	mainMux.Handle("/metrics", auth.RequireAPIKey(metricsKey, promhttp.Handler().ServeHTTP))

	mainMux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		h := redisclient.CheckHealth(r.Context(), rdb, redisclient.Config{Addr: mr.Addr()})
		if !h.Connected {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "unhealthy",
				"redis":  h,
			})
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "healthy",
			"redis":  h,
		})
	})

	mainMux.HandleFunc("/check", auth.RequireAPIKey(cfg.InternalAPIKey, func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx := r.Context()
		userID, err := identity.ResolveUserID(r, cfg.AllowQueryUserID)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		if r.URL.Query().Get("idempotent_replay") == "true" {
			w.Header().Set("Content-Type", "application/json")
			setRateLimitHeaders(w, rateLimitLimitHeader(cfg), cfg.Capacity)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"allowed":   true,
				"remaining": cfg.Capacity,
				"replay":    true,
			})
			return
		}
		if !checkRedisCircuit(ctx, w, oteltrace.SpanFromContext(ctx)) {
			return
		}
		allowed, remaining, err := limiterInstance.Allow(ctx, userID)
		recordRedisCircuit(ctx, err, start)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": "Rate limiter unavailable"})
			return
		}
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
	}))

	if cfg.EnableHierarchical {
		mainMux.HandleFunc("/check_hierarchical", auth.RequireAPIKey(cfg.InternalAPIKey, func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ctx := r.Context()
			userID, err := identity.ResolveUserID(r, cfg.AllowQueryUserID)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
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
			if r.URL.Query().Get("idempotent_replay") == "true" {
				capacities, _ := effectiveHierarchicalLimits(r.Context(), cfg, overrideStore, tenantID, userID, endpoint)
				limitHeader := effectiveHierarchicalLimitHeader(capacities)
				remaining := capacities[0]
				for _, cap := range capacities[1:] {
					if cap < remaining {
						remaining = cap
					}
				}
				w.Header().Set("Content-Type", "application/json")
				setRateLimitHeaders(w, limitHeader, remaining)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"allowed":   true,
					"remaining": remaining,
					"replay":    true,
				})
				return
			}
			if !checkRedisCircuit(ctx, w, oteltrace.SpanFromContext(ctx)) {
				return
			}
			globalKey := "rate:global"
			tenantKey := fmt.Sprintf("rate:tenant:%s", tenantID)
			userKey := fmt.Sprintf("rate:user:%s", userID)
			endpointKey := fmt.Sprintf("rate:endpoint:%s:%s", tenantID, endpoint)
			capacities, refillRates := effectiveHierarchicalLimits(r.Context(), cfg, overrideStore, tenantID, userID, endpoint)
			allowed, remaining, err := hierarchicalLimiter.AllowWithParams(
				ctx,
				[]string{globalKey, tenantKey, userKey, endpointKey},
				capacities,
				refillRates,
			)
			recordRedisCircuit(ctx, err, start)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				json.NewEncoder(w).Encode(map[string]string{"error": "Hierarchical rate limiter unavailable"})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			setRateLimitHeaders(w, effectiveHierarchicalLimitHeader(capacities), remaining)
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
		}))
	}

	mainSrv := httptest.NewServer(mainMux)

	// Setup admin mux
	adminMux := http.NewServeMux()
	registerLimitRoutes(adminMux, cfg, overrideStore, "user", cfg.UserCapacity, cfg.UserRefillRate)
	registerLimitRoutes(adminMux, cfg, overrideStore, "tenant", cfg.TenantCapacity, cfg.TenantRefillRate)
	registerLimitRoutes(adminMux, cfg, overrideStore, "endpoint", cfg.EndpointCapacity, cfg.EndpointRefillRate)
	registerLimitRoutes(adminMux, cfg, overrideStore, "global", cfg.GlobalCapacity, cfg.GlobalRefillRate)
	registerIdempotencyRoutes(adminMux, cfg, rdb)
	registerRoutingRoutes(adminMux, cfg, rdb)
	registerCircuitRoutes(adminMux, cfg, rdb, redisCircuit)
	registerAuditRoutes(adminMux, cfg, auditStore)

	adminSrv := httptest.NewServer(adminMux)

	fixture := &testFixture{
		cfg:      cfg,
		mr:       mr,
		rdb:      rdb,
		mainSrv:  mainSrv,
		adminSrv: adminSrv,
		mainURL:  mainSrv.URL,
		adminURL: adminSrv.URL,
	}

	cleanup := func() {
		mainSrv.Close()
		adminSrv.Close()
		_ = rdb.Close()
		mr.Close()
	}

	return fixture, cleanup
}

func captureRedisSnapshot(t *testing.T, rdb redis.UniversalClient) map[string]string {
	t.Helper()
	ctx := context.Background()
	keys, err := rdb.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatalf("snapshot Keys failed: %v", err)
	}
	snap := make(map[string]string)
	for _, k := range keys {
		typ, err := rdb.Type(ctx, k).Result()
		if err != nil {
			t.Fatalf("snapshot Type failed for key %s: %v", k, err)
		}
		switch typ {
		case "string":
			val, _ := rdb.Get(ctx, k).Result()
			snap[k] = "string:" + val
		case "hash":
			val, _ := rdb.HGetAll(ctx, k).Result()
			jsonVal, _ := json.Marshal(val)
			snap[k] = "hash:" + string(jsonVal)
		case "set":
			val, _ := rdb.SMembers(ctx, k).Result()
			jsonVal, _ := json.Marshal(val)
			snap[k] = "set:" + string(jsonVal)
		case "zset":
			val, _ := rdb.ZRangeWithScores(ctx, k, 0, -1).Result()
			jsonVal, _ := json.Marshal(val)
			snap[k] = "zset:" + string(jsonVal)
		default:
			snap[k] = typ
		}
	}
	return snap
}

func assertRedisSnapshotEqual(t *testing.T, before, after map[string]string) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("Redis state mutated: before has %d keys, after has %d keys", len(before), len(after))
	}
	for k, vBefore := range before {
		vAfter, ok := after[k]
		if !ok {
			t.Fatalf("Redis state mutated: key %q deleted", k)
		}
		if vBefore != vAfter {
			t.Fatalf("Redis state mutated: key %q changed from %q to %q", k, vBefore, vAfter)
		}
	}
}
