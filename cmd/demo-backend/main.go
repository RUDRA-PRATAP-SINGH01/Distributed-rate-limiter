// Minimal upstream target for sidecar demos and load tests.
// In production this would be your real API; here it proves the proxy path end-to-end.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync/atomic"
)

var orderExecutions int64

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	http.HandleFunc("/api/orders", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&orderExecutions, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"order_id":    "ord_demo",
			"execution":   n,
			"idempotency": "upstream executed",
		})
	})

	http.HandleFunc("/api/orders/count", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int64{"executions": atomic.LoadInt64(&orderExecutions)})
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": "Hello from backend"})
	})
	log.Printf("Demo backend on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
