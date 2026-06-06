package auth

import "net/http"

const APIKeyHeader = "X-API-Key"
const InternalAPIKeyHeader = "X-Internal-API-Key"

// RequireAPIKey wraps a handler and rejects requests when the key does not match.
// When expectedKey is empty the middleware is a no-op (dev / internal-network mode).
func RequireAPIKey(expectedKey string, next http.HandlerFunc) http.HandlerFunc {
	if expectedKey == "" {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get(APIKeyHeader)
		if key == "" {
			key = r.Header.Get(InternalAPIKeyHeader)
		}
		if key != expectedKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}
