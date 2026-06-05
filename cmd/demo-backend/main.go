package main

import (
	"encoding/json"
	"log"
	"net/http"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": "Hello from backend"})
	})
	log.Println("Demo backend on :8081")
	log.Fatal(http.ListenAndServe(":8081", nil))
}
