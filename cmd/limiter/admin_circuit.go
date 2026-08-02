package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/auth"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/circuitbreaker"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/logging"
	"github.com/redis/go-redis/v9"
)

// registerCircuitRoutes exposes circuit state for ops.
//
// target=redis is served from redisBreaker (LocalStore) so state remains
// readable/resettable while Redis is down. Other targets use Redis-backed state.
func registerCircuitRoutes(mux *http.ServeMux, cfg Config, rdb redis.UniversalClient, redisBreaker *circuitbreaker.Breaker) {
	var redisStore *circuitbreaker.RedisStore
	if rdb != nil {
		redisStore = circuitbreaker.NewRedisStore(rdb, circuitbreaker.LoadConfigFromEnv())
	}
	prefix := "/admin/circuit/"

	mux.HandleFunc("/admin/circuit", func(w http.ResponseWriter, r *http.Request) {
		if !auth.SecureCompare(r.Header.Get(auth.APIKeyHeader), cfg.AdminAPIKey) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var snaps []circuitbreaker.Snapshot
		if redisBreaker != nil {
			local, err := redisBreaker.List(r.Context())
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			snaps = append(snaps, local...)
		}
		if redisStore != nil {
			remote, err := redisStore.ListTargets(r.Context())
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			for _, s := range remote {
				if s.Target == circuitbreaker.TargetRedis {
					continue // local breaker is authoritative for redis
				}
				snaps = append(snaps, s)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(snaps)
	})

	mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
		if !auth.SecureCompare(r.Header.Get(auth.APIKeyHeader), cfg.AdminAPIKey) {
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
			snap, err := getCircuitSnapshot(r, target, redisBreaker, redisStore)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(snap)
		case http.MethodDelete:
			if err := resetCircuit(r, target, redisBreaker, redisStore); err != nil {
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

func getCircuitSnapshot(r *http.Request, target string, redisBreaker *circuitbreaker.Breaker, redisStore *circuitbreaker.RedisStore) (circuitbreaker.Snapshot, error) {
	if target == circuitbreaker.TargetRedis && redisBreaker != nil {
		return redisBreaker.GetState(r.Context(), target)
	}
	if redisStore == nil {
		return circuitbreaker.Snapshot{Target: target, State: circuitbreaker.StateClosed}, nil
	}
	return redisStore.GetState(r.Context(), target)
}

func resetCircuit(r *http.Request, target string, redisBreaker *circuitbreaker.Breaker, redisStore *circuitbreaker.RedisStore) error {
	if target == circuitbreaker.TargetRedis && redisBreaker != nil {
		return redisBreaker.Reset(r.Context(), target)
	}
	if redisStore == nil {
		return nil
	}
	return redisStore.Reset(r.Context(), target)
}
