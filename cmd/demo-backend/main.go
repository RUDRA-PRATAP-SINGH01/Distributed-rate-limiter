// Minimal upstream target for sidecar demos and load tests.
// In production this would be your real API; here it proves the proxy path end-to-end.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": "Hello from backend"})
	})
	log.Printf("Demo backend on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
