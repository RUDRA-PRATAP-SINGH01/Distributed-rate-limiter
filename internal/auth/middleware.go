// Package auth provides lightweight HTTP middleware for shared-secret authentication.
// Used between sidecars and the central limiter, and optionally on /metrics.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
)

const APIKeyHeader = "X-API-Key"
const InternalAPIKeyHeader = "X-Internal-API-Key"

// SecureCompare performs a constant-time comparison between key and expectedKey.
// Both inputs are hashed with SHA-256 to produce fixed 32-byte digests before
// comparison, completely eliminating length and timing side-channel oracles (M-05).
func SecureCompare(key, expectedKey string) bool {
	// Empty expected is a server misconfig, not an attacker-controlled input.
	// Presented keys (including empty) always take the hash path so length is
	// not observable from an early return.
	if expectedKey == "" {
		return false
	}
	keyHash := sha256.Sum256([]byte(key))
	expectedHash := sha256.Sum256([]byte(expectedKey))
	return subtle.ConstantTimeCompare(keyHash[:], expectedHash[:]) == 1
}

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
		if !SecureCompare(key, expectedKey) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}
