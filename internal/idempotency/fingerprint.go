package idempotency

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// IsMutatingMethod reports whether the HTTP method should participate in idempotency.
func IsMutatingMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	default:
		return false
	}
}

// Fingerprint hashes method, path, and body so the same key cannot be reused with different payloads.
func Fingerprint(method, path string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(strings.ToUpper(method)))
	h.Write([]byte{'\n'})
	h.Write([]byte(path))
	h.Write([]byte{'\n'})
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// BuildScope isolates keys per tenant and user so keys never collide across tenants.
func BuildScope(tenantID, userID string) string {
	if tenantID == "" {
		tenantID = "default"
	}
	h := sha256.Sum256([]byte(tenantID + "|" + userID))
	return hex.EncodeToString(h[:16]) // 32-char hex scope prefix
}
