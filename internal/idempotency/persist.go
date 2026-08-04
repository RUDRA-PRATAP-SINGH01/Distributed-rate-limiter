package idempotency

import "net/http"

// PersistAsComplete reports whether an upstream HTTP status is an authoritative
// answer that may be stored for CompletedTTL (default 24h).
//
// Transient outcomes (no status, 408, 425, 5xx) must use Fail + LockTTL so a
// brief outage cannot poison the Idempotency-Key (N-04). Upstream 429 is
// Complete: the origin decided the request, and that decision is replayable.
func PersistAsComplete(status int) bool {
	if status <= 0 {
		return false
	}
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly:
		return false
	}
	return status < http.StatusInternalServerError
}
