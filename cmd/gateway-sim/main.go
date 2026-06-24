// Gateway simulator for intelligent routing demos.
// Each instance simulates a payment gateway with configurable latency and error rate.
package main

import (
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

var requests int64

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}
	gatewayID := os.Getenv("GATEWAY_ID")
	if gatewayID == "" {
		gatewayID = "gateway-default"
	}
	latencyMs := envInt("SIMULATED_LATENCY_MS", 20)
	errorRate := envFloat("SIMULATED_ERROR_RATE", 0.01)

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "healthy",
			"gateway": gatewayID,
		})
	})

	http.HandleFunc("/api/payments", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requests, 1)
		time.Sleep(time.Duration(latencyMs) * time.Millisecond)

		if rand.Float64() < errorRate {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]string{
				"error":   "gateway_error",
				"gateway": gatewayID,
			})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "captured",
			"gateway": gatewayID,
			"txn_id":  "txn_" + gatewayID,
		})
	})

	http.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"gateway":           gatewayID,
			"requests":          atomic.LoadInt64(&requests),
			"simulated_latency": latencyMs,
			"simulated_error":   errorRate,
		})
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"message": "gateway " + gatewayID,
			"gateway": gatewayID,
		})
	})

	log.Printf("Gateway %s on :%s (latency=%dms error_rate=%.2f)", gatewayID, port, latencyMs, errorRate)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func envInt(key string, def int) int {
	if raw := os.Getenv(key); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			return v
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if raw := os.Getenv(key); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil {
			return v
		}
	}
	return def
}
