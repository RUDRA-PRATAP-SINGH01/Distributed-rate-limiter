package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/circuitbreaker"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/logging"
	"github.com/redis/go-redis/v9"
)

func registerCircuitRoutes(mux *http.ServeMux, cfg Config, rdb redis.UniversalClient) {
	if rdb == nil {
		return
	}
	store := circuitbreaker.NewRedisStore(rdb, circuitbreaker.LoadConfigFromEnv())
	prefix := "/admin/circuit/"

	mux.HandleFunc("/admin/circuit", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != cfg.AdminAPIKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		snaps, err := store.ListTargets(r.Context())
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(snaps)
	})

	mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != cfg.AdminAPIKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		target := strings.TrimPrefix(r.URL.Path, prefix)
		target = strings.TrimSuffix(target, "/")
		if target == "" {
			http.Error(w, "missing target", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			snap, err := store.GetState(r.Context(), target)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(snap)
		case http.MethodDelete:
			if err := store.Reset(r.Context(), target); err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			logging.Info(r.Context(), "admin reset circuit breaker",
				"component", "admin",
				"action", "reset_circuit",
				"target", target,
			)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}
