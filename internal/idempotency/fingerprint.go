package idempotency

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
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

// Fingerprint hashes method, path, sorted query string, and body so the same key
// cannot be reused with different payloads (including differing query params).
func Fingerprint(method, path, rawQuery string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(strings.ToUpper(method)))
	h.Write([]byte{'\n'})
	h.Write([]byte(path))
	h.Write([]byte{'\n'})
	h.Write([]byte(sortedQuery(rawQuery)))
	h.Write([]byte{'\n'})
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

func sortedQuery(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	vals, err := url.ParseQuery(rawQuery)
	if err != nil {
		return rawQuery
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		sortedVals := append([]string(nil), vals[k]...)
		sort.Strings(sortedVals)
		for j, v := range sortedVals {
			if j > 0 {
				b.WriteByte('&')
			}
			b.WriteString(url.QueryEscape(k))
			b.WriteByte('=')
			b.WriteString(url.QueryEscape(v))
		}
	}
	return b.String()
}

// BuildScope isolates keys per tenant and user so keys never collide across tenants.
func BuildScope(tenantID, userID string) string {
	if tenantID == "" {
		tenantID = "default"
	}
	h := sha256.Sum256([]byte(tenantID + "|" + userID))
	return hex.EncodeToString(h[:16]) // 32-char hex scope prefix
}
