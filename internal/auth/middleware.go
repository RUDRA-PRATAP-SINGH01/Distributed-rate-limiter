// Package auth provides lightweight HTTP middleware for shared-secret authentication.
// Used between sidecars and the central limiter, and optionally on /metrics.
package auth

import (
	"crypto/subtle"
	"net/http"
)

const APIKeyHeader = "X-API-Key"
const InternalAPIKeyHeader = "X-Internal-API-Key"

// RequireAPIKey wraps a handler and rejects requests when the key does not match.
// Empty expectedKey disables the check — useful for local Prometheus scraping.
func RequireAPIKey(expectedKey string, next http.HandlerFunc) http.HandlerFunc {
	if expectedKey == "" {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get(APIKeyHeader)
		if key == "" {
			key = r.Header.Get(InternalAPIKeyHeader)
		}
		// ConstantTimeCompare prevents timing side-channel attacks on the API key comparison.
		if subtle.ConstantTimeCompare([]byte(key), []byte(expectedKey)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}
