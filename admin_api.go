package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/override"
)

func startAdminServer(cfg Config, store *override.Store) *http.Server {
	if !cfg.EnableAdminAPI {
		return nil
	}

	mux := http.NewServeMux()
	registerLimitRoutes(mux, cfg, store, "user", cfg.UserCapacity, cfg.UserRefillRate)
	registerLimitRoutes(mux, cfg, store, "tenant", cfg.TenantCapacity, cfg.TenantRefillRate)
	registerLimitRoutes(mux, cfg, store, "endpoint", cfg.EndpointCapacity, cfg.EndpointRefillRate)
	registerLimitRoutes(mux, cfg, store, "global", cfg.GlobalCapacity, cfg.GlobalRefillRate)

	srv := &http.Server{
		Addr:         cfg.AdminAddr(),
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("Admin API listening on %s", cfg.AdminAddr())
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Admin server error: %v", err)
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
	if r.Header.Get("X-API-Key") != apiKey {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		cfg, found := getOverrideByLevel(store, level, id)
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
		if err := store.SetOverride(level, id, newCfg); err != nil {
			log.Printf("[admin] set override error: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		log.Printf("[admin] set %s override for %s: capacity=%d refill_rate=%.2f", level, id, newCfg.Capacity, newCfg.RefillRate)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := store.DeleteOverride(level, id); err != nil {
			log.Printf("[admin] delete override error: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		log.Printf("[admin] deleted %s override for %s", level, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func getOverrideByLevel(store *override.Store, level, id string) (override.Config, bool) {
	switch level {
	case "global":
		return store.GetGlobalOverride()
	case "user":
		return store.GetUserOverride(id)
	case "tenant":
		return store.GetTenantOverride(id)
	case "endpoint":
		return store.GetEndpointOverride(parseEndpointOverrideTenant(id), parseEndpointOverridePath(id))
	default:
		return override.Config{}, false
	}
}

func effectiveHierarchicalLimits(cfg Config, store *override.Store, tenantID, userID, endpoint string) ([]int, []float64) {
	globalCap, globalRate := cfg.GlobalCapacity, cfg.GlobalRefillRate
	tenantCap, tenantRate := cfg.TenantCapacity, cfg.TenantRefillRate
	userCap, userRate := cfg.UserCapacity, cfg.UserRefillRate
	endpointCap, endpointRate := cfg.EndpointCapacity, cfg.EndpointRefillRate

	if ov, ok := store.GetGlobalOverride(); ok {
		globalCap, globalRate = ov.Capacity, ov.RefillRate
	}
	if ov, ok := store.GetTenantOverride(tenantID); ok {
		tenantCap, tenantRate = ov.Capacity, ov.RefillRate
	}
	if ov, ok := store.GetUserOverride(userID); ok {
		userCap, userRate = ov.Capacity, ov.RefillRate
	}
	if ov, ok := store.GetEndpointOverride(tenantID, endpoint); ok {
		endpointCap, endpointRate = ov.Capacity, ov.RefillRate
	}

	return []int{globalCap, tenantCap, userCap, endpointCap},
		[]float64{globalRate, tenantRate, userRate, endpointRate}
}

func effectiveHierarchicalLimitHeader(capacities []int) string {
	limit := capacities[0]
	for _, cap := range capacities[1:] {
		if cap < limit {
			limit = cap
		}
	}
	return fmt.Sprintf("%d", limit)
}

// Endpoint override admin IDs use "tenant|/path" (pipe-separated, URL-encoded in path).
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
