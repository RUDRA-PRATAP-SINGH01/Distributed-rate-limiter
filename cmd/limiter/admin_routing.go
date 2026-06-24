package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/circuitbreaker"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/routing"
	"github.com/redis/go-redis/v9"
)

func registerRoutingRoutes(mux *http.ServeMux, cfg Config, rdb redis.UniversalClient) {
	if rdb == nil {
		return
	}
	routeCfg := routing.LoadConfigFromEnv()
	cbCfg := circuitbreaker.LoadConfigFromEnv()
	breaker := circuitbreaker.NewBreaker(circuitbreaker.NewRedisStore(rdb, cbCfg))
	store := routing.NewRedisStore(rdb, routeCfg)
	store.SetBreaker(breaker)
	prefix := "/admin/routing/gateways/"

	mux.HandleFunc("/admin/routing/gateways", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != cfg.AdminAPIKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		states, err := store.ListGateways(r.Context())
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		cfg := routing.LoadConfigFromEnv()
		type scored struct {
			routing.GatewayState
			RoutingScore float64 `json:"routing_score"`
			ErrorRate    float64 `json:"error_rate"`
		}
		out := make([]scored, 0, len(states))
		for _, st := range states {
			out = append(out, scored{
				GatewayState: st,
				RoutingScore: routing.ComputeScore(st, cfg),
				ErrorRate:    st.ErrorRate(),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	})

	mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != cfg.AdminAPIKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, prefix)
		id = strings.TrimSuffix(id, "/")
		if id == "" {
			http.Error(w, "missing gateway id", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			gw, err := store.GetGateway(r.Context(), id)
			if err != nil || gw == nil {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			cfg := routing.LoadConfigFromEnv()
			json.NewEncoder(w).Encode(map[string]interface{}{
				"gateway":       gw,
				"routing_score": routing.ComputeScore(*gw, cfg),
				"error_rate":    gw.ErrorRate(),
			})
		case http.MethodPost:
			var body struct {
				Weight  *int  `json:"weight"`
				Enabled *bool `json:"enabled"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			if body.Weight != nil {
				_ = store.SetWeight(r.Context(), id, *body.Weight)
			}
			if body.Enabled != nil {
				_ = store.SetEnabled(r.Context(), id, *body.Enabled)
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			_ = store.ResetCircuit(r.Context(), id)
			log.Printf("[admin] reset circuit for gateway %s", id)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}
