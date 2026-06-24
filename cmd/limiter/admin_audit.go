package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/audit"
)

func registerAuditRoutes(mux *http.ServeMux, cfg Config, store *audit.Store) {
	if store == nil {
		return
	}

	mux.HandleFunc("/admin/audit/stats", func(w http.ResponseWriter, r *http.Request) {
		if !auditAuth(w, r, cfg) {
			return
		}
		stats, err := store.Stats(r.Context())
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, stats)
	})

	mux.HandleFunc("/admin/audit/replay", func(w http.ResponseWriter, r *http.Request) {
		if !auditAuth(w, r, cfg) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		payload, err := store.Replay(r.Context(), id)
		if err != nil || payload == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeJSON(w, payload)
	})

	mux.HandleFunc("/admin/audit", func(w http.ResponseWriter, r *http.Request) {
		if !auditAuth(w, r, cfg) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		q := parseAuditQuery(r)
		events, err := store.Search(r.Context(), q)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		stats, _ := store.Stats(r.Context())
		writeJSON(w, map[string]interface{}{"events": events, "count": len(events), "stats": stats})
	})

	mux.HandleFunc("/admin/audit/", func(w http.ResponseWriter, r *http.Request) {
		if !auditAuth(w, r, cfg) {
			return
		}
		id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/admin/audit/"), "/")
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		if strings.HasSuffix(id, "/replay") {
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			eventID := strings.TrimSuffix(id, "/replay")
			payload, err := store.Replay(r.Context(), eventID)
			if err != nil || payload == nil {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			writeJSON(w, payload)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ev, err := store.Get(r.Context(), id)
		if err != nil || ev == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeJSON(w, ev)
	})

	log.Printf("[admin] audit trail API registered")
}

func auditAuth(w http.ResponseWriter, r *http.Request, cfg Config) bool {
	if r.Header.Get("X-API-Key") != cfg.AdminAPIKey {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func parseAuditQuery(r *http.Request) audit.Query {
	q := audit.Query{
		RequestID: r.URL.Query().Get("request_id"),
		TenantID:  r.URL.Query().Get("tenant_id"),
		UserID:    r.URL.Query().Get("user_id"),
		Decision:  audit.Decision(r.URL.Query().Get("decision")),
		Handler:   r.URL.Query().Get("handler"),
	}
	if v := r.URL.Query().Get("from_ms"); v != "" {
		q.FromMs, _ = strconv.ParseInt(v, 10, 64)
	}
	if v := r.URL.Query().Get("to_ms"); v != "" {
		q.ToMs, _ = strconv.ParseInt(v, 10, 64)
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			q.Limit = n
		}
	}
	return q
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
