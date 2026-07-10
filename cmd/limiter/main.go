// Package main is the central rate limiter service.
//
// This process owns the authoritative quota state in Redis. Sidecars call it over HTTP;
// they may cache denials locally but never own the source of truth. That split keeps
// enforcement consistent across a fleet while still cutting latency on hot paths.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/audit"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/auth"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/circuitbreaker"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/identity"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/limiter"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/logging"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/metrics"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/override"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/telemetry"
	redisclient "github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/redis"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
)

var limiterInstance RateLimiter
var hierarchicalLimiter *limiter.HierarchicalLimiter
var overrideStore *override.Store
var redisCircuit *circuitbreaker.Breaker
var auditStore *audit.Store
var redisCfg redisclient.Config

func main() {
	logging.Init()
	cfg := LoadConfig()

	otelCfg := telemetry.LoadConfigFromEnv("rate-limiter")
	otelShutdown, err := telemetry.Init(context.Background(), otelCfg)
	if err != nil {
		logging.Fatal("OpenTelemetry init failed", "error", err)
	}

	// Fail fast if Redis is down. A limiter that starts without verified connectivity
	// would either panic on first request or silently mis-report health.
	redisCfg = redisclient.LoadConfigFromEnv()
	rdb := redisclient.New(redisCfg)
	if err := redisclient.Ping(rdb); err != nil {
		logging.Fatal("Redis unreachable", "redis", redisclient.Describe(redisCfg), "error", err)
	}
	if otelCfg.Enabled {
		if err := telemetry.InstrumentRedis(rdb); err != nil {
			logging.Fatal("Redis OpenTelemetry instrumentation failed", "error", err)
		}
	}
	logging.Info(nil, "Redis connection verified", "component", "limiter", "redis", redisclient.Describe(redisCfg))

	auditCfg := audit.LoadConfigFromEnv()
	auditStore = audit.NewStore(rdb, auditCfg)
	if auditCfg.Enabled {
		logging.Info(nil, "Audit trail enabled",
			"component", "limiter",
			"retention", auditCfg.Retention.String(),
			"max_events", auditCfg.MaxEvents,
		)
	}

	cbCfg := circuitbreaker.LoadConfigFromEnv()
	redisCircuit = circuitbreaker.NewBreaker(circuitbreaker.NewRedisStore(rdb, cbCfg))
	logging.Info(nil, "Redis circuit breaker enabled",
		"component", "limiter",
		"failure_rate", cbCfg.FailureRateThreshold,
		"cooldown_ms", cbCfg.OpenCooldownMs,
	)

	// Override store sits beside the algorithm layer: limits can be tuned at runtime
	// without redeploying binaries. Local TTL cache avoids a Redis round-trip per check.
	overrideStore = override.NewStore(rdb, time.Duration(cfg.OverrideCacheTTLMs)*time.Millisecond)

	// Algorithm is swappable via env. Token bucket gives smooth refill; sliding window
	// gives hard per-window caps. Both use Lua so increment + read is atomic cluster-wide.
	switch cfg.Algorithm {
	case "sliding":
		limiterInstance = limiter.NewRedisSlidingWindow(rdb, cfg.Capacity, time.Duration(cfg.WindowSec)*time.Second)
		logging.Info(nil, "Using Redis sliding window algorithm",
			"component", "limiter",
			"algorithm", "sliding_window",
			"capacity", cfg.Capacity,
			"window_sec", cfg.WindowSec,
		)
	case "token":
		fallthrough
	default:
		limiterInstance = limiter.NewRedisAtomicTokenBucket(rdb, cfg.Capacity, cfg.RefillRate)
		logging.Info(nil, "Using Redis token bucket algorithm",
			"component", "limiter",
			"algorithm", "token_bucket",
		)
	}

	if cfg.EnableHierarchical {
		hierarchicalLimiter = limiter.NewHierarchicalLimiter(
			rdb,
			cfg.GlobalCapacity, cfg.TenantCapacity, cfg.UserCapacity, cfg.EndpointCapacity,
			cfg.GlobalRefillRate, cfg.TenantRefillRate, cfg.UserRefillRate, cfg.EndpointRefillRate,
		)
		logging.Info(nil, "Hierarchical limiter enabled",
			"component", "limiter",
			"algorithm", "hierarchical",
		)
	}

	metricsKey := ""
	if cfg.MetricsRequireAuth {
		metricsKey = cfg.MetricsAuthKey()
	}
	mux := http.NewServeMux()

	// Metrics stay open by default so Prometheus can scrape without bearer tokens.
	// Flip METRICS_REQUIRE_AUTH in production if the endpoint is on a public network.
	mux.Handle("/metrics", auth.RequireAPIKey(metricsKey, promhttp.Handler().ServeHTTP))

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		h := redisclient.CheckHealth(r.Context(), rdb, redisCfg)
		if !h.Connected {
			logRedisHealthTransition(r.Context(), false, h.Error)
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "unhealthy",
				"redis":  h,
			})
			return
		}
		logRedisHealthTransition(r.Context(), true, "")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "healthy",
			"redis":  h,
		})
	})

	// /check is the flat per-user limiter path. Protected by INTERNAL_API_KEY so only
	// trusted sidecars (or internal services) can consume quota on behalf of users.
	mux.HandleFunc("/check", auth.RequireAPIKey(cfg.InternalAPIKey, func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx := r.Context()
		ctx, span := telemetry.StartSpan(ctx, "limiter.check",
			attribute.String("limiter.handler", "check"),
		)
		defer span.End()

		userID, err := identity.ResolveUserID(r, cfg.AllowQueryUserID)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			telemetry.SetHTTPStatus(span, http.StatusBadRequest)
			return
		}

		if r.URL.Query().Get("idempotent_replay") == "true" {
			metrics.RecordRequestDuration("check", time.Since(start).Seconds())
			w.Header().Set("Content-Type", "application/json")
			setRateLimitHeaders(w, rateLimitLimitHeader(cfg), cfg.Capacity)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"allowed":   true,
				"remaining": cfg.Capacity,
				"replay":    true,
			})
			span.SetAttributes(attribute.Bool("idempotent.replay", true))
			return
		}

		if !checkRedisCircuit(ctx, w, span) {
			elapsed := time.Since(start).Seconds()
			metrics.RecordRequestDuration("check", elapsed)
			metrics.RecordDependencyFailure("circuit_redis", "check", elapsed)
			telemetry.SetHTTPStatus(span, http.StatusServiceUnavailable)
			return
		}

		allowed, remaining, err := limiterInstance.Allow(ctx, userID)
		recordRedisCircuit(ctx, err, start)
		if err != nil {
			elapsed := time.Since(start).Seconds()
			metrics.RecordRequestDuration("check", elapsed)
			metrics.RecordDependencyFailure("redis", "check", elapsed)
			telemetry.RecordError(span, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "Rate limiter unavailable",
			})
			recordAudit(ctx, auditStore, audit.RecordInput{
				UserID: userID, Decision: audit.DecisionError,
				Reason: "check: redis unavailable", Handler: "check",
			})
			return
		}

		metrics.RecordRequest("check", allowed)
		metrics.RecordRequestDuration("check", time.Since(start).Seconds())
		span.SetAttributes(
			attribute.Bool("rate_limit.allowed", allowed),
			attribute.Int("rate_limit.remaining", remaining),
		)

		w.Header().Set("Content-Type", "application/json")
		setRateLimitHeaders(w, rateLimitLimitHeader(cfg), remaining)

		if !allowed {
			w.Header().Set("Retry-After", retryAfterForCheck(cfg))
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]string{"error": "Too many requests"})
			telemetry.SetHTTPStatus(span, http.StatusTooManyRequests)
			recordAudit(ctx, auditStore, audit.RecordInput{
				UserID: userID, Decision: audit.DecisionDenied,
				Reason: audit.ReasonFor(false, "check"), Handler: "check", Remaining: remaining,
			})
			return
		}
		recordAudit(ctx, auditStore, audit.RecordInput{
			UserID: userID, Decision: audit.DecisionAllowed,
			Reason: audit.ReasonFor(true, "check"), Handler: "check", Remaining: remaining,
		})
		json.NewEncoder(w).Encode(map[string]interface{}{
			"allowed":   true,
			"remaining": remaining,
		})
	}))

	if cfg.EnableHierarchical {
		// Hierarchical check enforces four independent buckets in one Lua round-trip.
		// A request must pass every level; remaining reflects the tightest constraint.
		mux.HandleFunc("/check_hierarchical", auth.RequireAPIKey(cfg.InternalAPIKey, func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ctx := r.Context()
			ctx, span := telemetry.StartSpan(ctx, "limiter.check_hierarchical",
				attribute.String("limiter.handler", "hierarchical"),
			)
			defer span.End()

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
				metrics.RecordRequest("hierarchical", true)
				metrics.RecordRequestDuration("hierarchical", time.Since(start).Seconds())
				w.Header().Set("Content-Type", "application/json")
				setRateLimitHeaders(w, limitHeader, remaining)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"allowed":   true,
					"remaining": remaining,
					"replay":    true,
				})
				return
			}

			if !checkRedisCircuit(ctx, w, span) {
				elapsed := time.Since(start).Seconds()
				metrics.RecordRequestDuration("hierarchical", elapsed)
				metrics.RecordDependencyFailure("circuit_redis", "hierarchical", elapsed)
				return
			}

			// Keys are namespaced so tenants and endpoints never share Redis state accidentally.
			globalKey := "rate:global"
			tenantKey := fmt.Sprintf("rate:tenant:%s", tenantID)
			userKey := fmt.Sprintf("rate:user:%s", userID)
			endpointKey := fmt.Sprintf("rate:endpoint:%s:%s", tenantID, endpoint)

			capacities, refillRates := effectiveHierarchicalLimits(r.Context(), cfg, overrideStore, tenantID, userID, endpoint)
			span.SetAttributes(
				attribute.String("tenant.id", tenantID),
				attribute.String("endpoint", endpoint),
			)
			allowed, remaining, err := hierarchicalLimiter.AllowWithParams(
				ctx,
				[]string{globalKey, tenantKey, userKey, endpointKey},
				capacities,
				refillRates,
			)
			recordRedisCircuit(ctx, err, start)
			if err != nil {
				elapsed := time.Since(start).Seconds()
				metrics.RecordRequestDuration("hierarchical", elapsed)
				metrics.RecordDependencyFailure("redis", "hierarchical", elapsed)
				telemetry.RecordError(span, err)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				json.NewEncoder(w).Encode(map[string]string{
					"error": "Hierarchical rate limiter unavailable",
				})
				recordAudit(ctx, auditStore, audit.RecordInput{
					TenantID: tenantID, UserID: userID, Decision: audit.DecisionError,
					Reason: "hierarchical: redis unavailable", Handler: "hierarchical",
				})
				return
			}

			metrics.RecordRequest("hierarchical", allowed)
			metrics.RecordRequestDuration("hierarchical", time.Since(start).Seconds())
			span.SetAttributes(
				attribute.Bool("rate_limit.allowed", allowed),
				attribute.Int("rate_limit.remaining", remaining),
			)

			w.Header().Set("Content-Type", "application/json")
			setRateLimitHeaders(w, effectiveHierarchicalLimitHeader(capacities), remaining)

			if !allowed {
				w.Header().Set("Retry-After", retryAfterForHierarchical(cfg))
				w.WriteHeader(http.StatusTooManyRequests)
				json.NewEncoder(w).Encode(map[string]string{"error": "Too many requests (hierarchical limit)"})
				recordAudit(ctx, auditStore, audit.RecordInput{
					TenantID: tenantID, UserID: userID, Decision: audit.DecisionDenied,
					Reason: audit.ReasonFor(false, "hierarchical"), Handler: "hierarchical", Remaining: remaining,
				})
				return
			}
			recordAudit(ctx, auditStore, audit.RecordInput{
				TenantID: tenantID, UserID: userID, Decision: audit.DecisionAllowed,
				Reason: audit.ReasonFor(true, "hierarchical"), Handler: "hierarchical", Remaining: remaining,
			})
			json.NewEncoder(w).Encode(map[string]interface{}{
				"allowed":   true,
				"remaining": remaining,
				"message":   "Request allowed by all levels",
			})
		}))
	}

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      telemetry.WrapHandler(mux, otelCfg.ServiceName),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Admin API runs on a separate port so override CRUD can be network-isolated from
	// the hot /check path in production (e.g. internal-only on Railway/K8s).
	adminSrv := startAdminServer(cfg, overrideStore, rdb, auditStore)

	go func() {
		logging.Info(nil, "Server starting", "component", "limiter", "port", cfg.Port)
		var err error
		if cfg.TLSCertFile != "" {
			err = srv.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			logging.Fatal("listen failed", "error", err)
		}
	}()

	// Graceful shutdown drains in-flight checks before exit — important during rolling deploys.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logging.Info(nil, "Shutting down server", "component", "limiter")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if adminSrv != nil {
		if err := adminSrv.Shutdown(ctx); err != nil {
			logging.Error(ctx, "Admin server shutdown error", "component", "limiter", "error", err)
		}
	}
	if err := srv.Shutdown(ctx); err != nil {
		logging.Fatal("Server forced to shutdown", "error", err)
	}
	if auditStore != nil && auditCfg.Enabled && auditCfg.Async {
		logging.Info(ctx, "Draining audit queue", "component", "limiter")
		if err := auditStore.Shutdown(ctx); err != nil {
			logging.Error(ctx, "Audit shutdown incomplete", "component", "limiter", "error", err)
		}
	}
	if err := otelShutdown(ctx); err != nil {
		logging.Error(ctx, "OpenTelemetry shutdown error", "component", "limiter", "error", err)
	}
	if auditStore != nil && !auditStore.RedisCloseSafe() {
		logging.Warn(ctx, "Skipping Redis close while audit workers are still active",
			"component", "limiter",
		)
	} else if err := redisclient.Close(rdb); err != nil {
		logging.Error(ctx, "Redis close error", "component", "limiter", "error", err)
	} else {
		logging.Info(ctx, "Redis client closed", "component", "limiter")
	}
	logging.Info(nil, "Server exited", "component", "limiter")
}
