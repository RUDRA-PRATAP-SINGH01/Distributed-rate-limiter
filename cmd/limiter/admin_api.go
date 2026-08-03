// Admin API: runtime override CRUD for hierarchical limits.
//
// Overrides are stored in Redis and merged into each /check_hierarchical call.
// This lets ops bump a tenant's quota during an incident without redeploying sidecars.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/audit"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/auth"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/logging"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/override"
	"github.com/redis/go-redis/v9"
)

func startAdminServer(cfg Config, store *override.Store, rdb redis.UniversalClient, auditTrail *audit.Store) *http.Server {
	if !cfg.EnableAdminAPI {
		return nil
	}

	mux := http.NewServeMux()
	registerLimitRoutes(mux, cfg, store, "user", cfg.UserCapacity, cfg.UserRefillRate)
	registerLimitRoutes(mux, cfg, store, "tenant", cfg.TenantCapacity, cfg.TenantRefillRate)
	registerLimitRoutes(mux, cfg, store, "endpoint", cfg.EndpointCapacity, cfg.EndpointRefillRate)
	registerLimitRoutes(mux, cfg, store, "global", cfg.GlobalCapacity, cfg.GlobalRefillRate)
	registerIdempotencyRoutes(mux, cfg, rdb)
	registerRoutingRoutes(mux, cfg, rdb)
	registerCircuitRoutes(mux, cfg, rdb, redisCircuit)
	registerAuditRoutes(mux, cfg, auditTrail)

	srv := &http.Server{
		Addr:         cfg.AdminAddr(),
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		ctx := context.Background()
		logging.Info(ctx, "Admin API listening", "component", "admin", "addr", cfg.AdminAddr())
		var err error
		if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
			err = srv.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logging.Error(ctx, "Admin server error", "component", "admin", "error", err)
		}
	}()

	return srv
}

func registerLimitRoutes(mux *http.ServeMux, cfg Config, store *override.Store, level string, defaultCap int, defaultRate float64) {
	prefix := "/admin/limits/" + level + "/"
	defaultCfg := override.Config{Capacity: defaultCap, RefillRate: defaultRate}
	defaultID := "default"
	if level == "global" {
		mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
			handleLimitOverride(w, r, cfg.AdminAPIKey, store, level, defaultID, defaultCfg)
		})
		return
	}

	mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, prefix)
		id = strings.TrimSuffix(id, "/")
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		handleLimitOverride(w, r, cfg.AdminAPIKey, store, level, id, defaultCfg)
	})
}

func handleLimitOverride(
	w http.ResponseWriter,
	r *http.Request,
	apiKey string,
	store *override.Store,
	level, id string,
	defaultCfg override.Config,
) {
	if !auth.SecureCompare(r.Header.Get(auth.APIKeyHeader), apiKey) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		cfg, found := getOverrideByLevel(r.Context(), store, level, id)
		if !found {
			cfg = defaultCfg
			w.Header().Set("X-Override-Applied", "false")
		} else {
			w.Header().Set("X-Override-Applied", "true")
		}
		json.NewEncoder(w).Encode(cfg)
	case http.MethodPost:
		var newCfg override.Config
		if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if newCfg.Capacity <= 0 {
			http.Error(w, "capacity must be > 0", http.StatusBadRequest)
			return
		}
		if newCfg.RefillRate <= 0 {
			newCfg.RefillRate = defaultCfg.RefillRate
		}
		if err := store.SetOverride(r.Context(), level, id, newCfg); err != nil {
			logging.Error(r.Context(), "admin set override failed",
				"component", "admin",
				"action", "set_override",
				"override_level", level,
				"error", err,
			)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		logging.Info(r.Context(), "admin set override",
			"component", "admin",
			"action", "set_override",
			"override_level", level,
			"capacity", newCfg.Capacity,
			"refill_rate", newCfg.RefillRate,
		)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := store.DeleteOverride(r.Context(), level, id); err != nil {
			logging.Error(r.Context(), "admin delete override failed",
				"component", "admin",
				"action", "delete_override",
				"override_level", level,
				"error", err,
			)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		logging.Info(r.Context(), "admin delete override",
			"component", "admin",
			"action", "delete_override",
			"override_level", level,
		)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func getOverrideByLevel(ctx context.Context, store *override.Store, level, id string) (override.Config, bool) {
	switch level {
	case "global":
		return store.GetGlobalOverride(ctx)
	case "user":
		return store.GetUserOverride(ctx, id)
	case "tenant":
		return store.GetTenantOverride(ctx, id)
	case "endpoint":
		return store.GetEndpointOverride(ctx, parseEndpointOverrideTenant(id), parseEndpointOverridePath(id))
	default:
		return override.Config{}, false
	}
}

// effectiveHierarchicalLimits merges static env defaults with Redis overrides.
// Each level is evaluated independently in Lua; this function only supplies the numbers.
func effectiveHierarchicalLimits(ctx context.Context, cfg Config, store *override.Store, tenantID, userID, endpoint string) ([]int, []float64) {
	globalCap, globalRate := cfg.GlobalCapacity, cfg.GlobalRefillRate
	tenantCap, tenantRate := cfg.TenantCapacity, cfg.TenantRefillRate
	userCap, userRate := cfg.UserCapacity, cfg.UserRefillRate
	endpointCap, endpointRate := cfg.EndpointCapacity, cfg.EndpointRefillRate

	if store != nil {
		store.RefreshGeneration(ctx)
		if ov, ok := store.GetGlobalOverride(ctx); ok {
			globalCap, globalRate = ov.Capacity, ov.RefillRate
		}
		if ov, ok := store.GetTenantOverride(ctx, tenantID); ok {
			tenantCap, tenantRate = ov.Capacity, ov.RefillRate
		}
		if ov, ok := store.GetUserOverride(ctx, userID); ok {
			userCap, userRate = ov.Capacity, ov.RefillRate
		}
		if ov, ok := store.GetEndpointOverride(ctx, tenantID, endpoint); ok {
			endpointCap, endpointRate = ov.Capacity, ov.RefillRate
		}
	}

	return []int{globalCap, tenantCap, userCap, endpointCap},
		[]float64{globalRate, tenantRate, userRate, endpointRate}
}

// X-RateLimit-Limit header shows the tightest capacity across all hierarchical levels
// so clients see the bottleneck they actually hit.
func effectiveHierarchicalLimitHeader(capacities []int) string {
	limit := capacities[0]
	for _, cap := range capacities[1:] {
		if cap < limit {
			limit = cap
		}
	}
	return fmt.Sprintf("%d", limit)
}

// Endpoint overrides are tenant-scoped: admin path id is "tenant|/path" (URL-encode the pipe).
func parseEndpointOverrideTenant(id string) string {
	for i := 0; i < len(id); i++ {
		if id[i] == '|' {
			return id[:i]
		}
	}
	return "default"
}

func parseEndpointOverridePath(id string) string {
	for i := 0; i < len(id); i++ {
		if id[i] == '|' {
			return id[i+1:]
		}
	}
	return id
}
