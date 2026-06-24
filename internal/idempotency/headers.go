package idempotency

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Whitelisted response headers replayed to clients (lowercase keys).
var replayHeaderAllowlist = map[string]struct{}{
	"content-type":     {},
	"x-request-id":     {},
	"x-correlation-id": {},
}

// FilterReplayHeaders keeps only safe, whitelisted headers for storage and replay.
func FilterReplayHeaders(h http.Header) map[string]string {
	out := make(map[string]string)
	for k, vals := range h {
		if _, ok := replayHeaderAllowlist[strings.ToLower(k)]; ok && len(vals) > 0 {
			out[k] = vals[0]
		}
	}
	return out
}

// ApplyReplayHeaders writes stored headers onto the response writer.
func ApplyReplayHeaders(w http.ResponseWriter, headers map[string]string) {
	for k, v := range headers {
		w.Header().Set(k, v)
	}
}

func encodeHeaders(h map[string]string) string {
	if len(h) == 0 {
		return "{}"
	}
	b, err := json.Marshal(h)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func decodeHeaders(raw string) map[string]string {
	if raw == "" || raw == "{}" {
		return map[string]string{}
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]string{}
	}
	return out
}

// WriteClaimResponse writes the appropriate HTTP response for a claim outcome.
func WriteClaimResponse(w http.ResponseWriter, claim *ClaimResponse) {
	switch claim.Result {
	case ResultReplay:
		w.Header().Set("X-Idempotency-Status", "replayed")
		ApplyReplayHeaders(w, claim.Headers)
		w.WriteHeader(claim.HTTPStatus)
		if len(claim.Body) > 0 {
			_, _ = w.Write(claim.Body)
		}
	case ResultInProgress:
		w.Header().Set("X-Idempotency-Status", "in_progress")
		if claim.RetryAfterMs > 0 {
			secs := claim.RetryAfterMs / 1000
			if secs < 1 {
				secs = 1
			}
			w.Header().Set("Retry-After", fmt.Sprintf("%d", secs))
		}
		http.Error(w, "request in progress", http.StatusConflict)
	case ResultHashMismatch:
		w.Header().Set("X-Idempotency-Status", "hash_mismatch")
		http.Error(w, "idempotency key reused with different request", http.StatusUnprocessableEntity)
	default:
		http.Error(w, "idempotency error", http.StatusInternalServerError)
	}
}

// SetCreatedHeader marks a freshly claimed request.
func SetCreatedHeader(w http.ResponseWriter) {
	w.Header().Set("X-Idempotency-Status", "created")
}

// ValidateKey checks client-provided idempotency key length.
func ValidateKey(key string) error {
	if len(key) == 0 {
		return fmt.Errorf("empty idempotency key")
	}
	if len(key) > MaxKeyLength {
		return ErrKeyTooLong
	}
	return nil
}

// NowMillis returns current unix time in milliseconds.
func NowMillis() int64 {
	return time.Now().UnixMilli()
}

// Store persists idempotency records in Redis via atomic Lua scripts.
type Store interface {
	Claim(ctx context.Context, scope, key, requestHash string) (*ClaimResponse, error)
	Complete(ctx context.Context, req CompleteRequest) error
	Fail(ctx context.Context, req FailRequest) error
}
