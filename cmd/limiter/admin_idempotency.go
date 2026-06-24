package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/idempotency"
	"github.com/redis/go-redis/v9"
)

func registerIdempotencyRoutes(mux *http.ServeMux, cfg Config, rdb *redis.Client) {
	if rdb == nil {
		return
	}
	store := idempotency.NewRedisStore(rdb, idempotency.DefaultConfig())
	prefix := "/admin/idempotency/"

	mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != cfg.AdminAPIKey {
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
				log.Printf("[admin] idempotency get error: %v", err)
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
				log.Printf("[admin] idempotency delete error: %v", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			log.Printf("[admin] deleted idempotency key scope=%s key=%s", scope, key)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Convenience lookup: ?tenant=acme&user=alice&key=pay-001
	mux.HandleFunc("/admin/idempotency", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != cfg.AdminAPIKey {
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
			log.Printf("[admin] idempotency lookup error: %v", err)
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
			"record":              rec,
			"lock_remaining_ms": rec.LockRemainingMs(),
		})
	})
}
