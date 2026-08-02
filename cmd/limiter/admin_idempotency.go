package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/auth"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/idempotency"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/logging"
	"github.com/redis/go-redis/v9"
)

func registerIdempotencyRoutes(mux *http.ServeMux, cfg Config, rdb redis.UniversalClient) {
	if rdb == nil {
		return
	}
	store := idempotency.NewRedisStore(rdb, idempotency.DefaultConfig())
	prefix := "/admin/idempotency/"

	mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
		if !auth.SecureCompare(r.Header.Get(auth.APIKeyHeader), cfg.AdminAPIKey) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		rest := strings.TrimPrefix(r.URL.Path, prefix)
		rest = strings.TrimSuffix(rest, "/")
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			http.Error(w, "path must be /admin/idempotency/{scope}/{key}", http.StatusBadRequest)
			return
		}
		scope, key := parts[0], parts[1]

		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			rec, err := store.GetRecord(r.Context(), scope, key)
			if err != nil {
				logging.Error(r.Context(), "admin idempotency get failed",
					"component", "admin",
					"action", "get_idempotency",
					"error", err,
				)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			if rec == nil {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			if rec.Headers == nil {
				rec.Headers = map[string]string{}
			}
			out := map[string]interface{}{
				"record":            rec,
				"lock_remaining_ms": rec.LockRemainingMs(),
			}
			json.NewEncoder(w).Encode(out)
		case http.MethodDelete:
			if err := store.DeleteRecord(r.Context(), scope, key); err != nil {
				logging.Error(r.Context(), "admin idempotency delete failed",
					"component", "admin",
					"action", "delete_idempotency",
					"key_present", true,
					"error", err,
				)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			logging.Info(r.Context(), "admin idempotency record deleted",
				"component", "admin",
				"action", "delete_idempotency",
				"key_present", true,
			)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Convenience lookup: ?tenant=acme&user=alice&key=pay-001
	mux.HandleFunc("/admin/idempotency", func(w http.ResponseWriter, r *http.Request) {
		if !auth.SecureCompare(r.Header.Get(auth.APIKeyHeader), cfg.AdminAPIKey) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		tenant := r.URL.Query().Get("tenant")
		user := r.URL.Query().Get("user")
		key := r.URL.Query().Get("key")
		if user == "" || key == "" {
			http.Error(w, "query params required: user, key (tenant optional)", http.StatusBadRequest)
			return
		}
		scope := idempotency.ScopeForTenantUser(tenant, user)
		rec, err := store.GetRecord(r.Context(), scope, key)
		if err != nil {
			logging.Error(r.Context(), "admin idempotency lookup failed",
				"component", "admin",
				"action", "lookup_idempotency",
				"key_present", key != "",
				"error", err,
			)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if rec == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"scope":             scope,
			"record":            rec,
			"lock_remaining_ms": rec.LockRemainingMs(),
		})
	})
}
