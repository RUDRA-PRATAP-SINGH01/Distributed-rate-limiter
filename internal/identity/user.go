// Package identity resolves who is consuming quota on a request.
//
// Production traffic should arrive with X-User-ID set by an auth gateway (JWT validated
// upstream). Query-string user_id / tenant_id is opt-in for local demos only.
package identity

import (
	"fmt"
	"net/http"
	"strings"
	"unicode"
)

const (
	UserIDHeader   = "X-User-ID"
	TenantIDHeader = "X-Tenant-ID"
	DefaultTenant  = "default"
	MaxIDLength    = 128
)

// ResolveUserID returns a trusted user identifier.
// allowQuery enables ?user_id= for development; disable it anywhere clients are untrusted.
func ResolveUserID(r *http.Request, allowQuery bool) (string, error) {
	if userID := strings.TrimSpace(r.Header.Get(UserIDHeader)); userID != "" {
		return userID, nil
	}
	if allowQuery {
		if userID := strings.TrimSpace(r.URL.Query().Get("user_id")); userID != "" {
			return userID, nil
		}
	}
	return "", fmt.Errorf("missing trusted user identity: set %s header", UserIDHeader)
}

// ResolveTenantID returns the hierarchical tenant. Query ?tenant_id= is gated by
// the same allowQuery flag as user identity (N-07). Missing tenant is "default".
func ResolveTenantID(r *http.Request, allowQuery bool) (string, error) {
	raw := strings.TrimSpace(r.Header.Get(TenantIDHeader))
	if raw == "" && allowQuery {
		raw = strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	}
	if raw == "" {
		return DefaultTenant, nil
	}
	if err := ValidateID("tenant id", raw); err != nil {
		return "", err
	}
	return raw, nil
}

// ValidateID rejects empty, oversized, or charset-unsafe identifiers so they
// cannot inject Redis keys or hash tags.
func ValidateID(kind, id string) error {
	if id == "" {
		return fmt.Errorf("empty %s", kind)
	}
	if len(id) > MaxIDLength {
		return fmt.Errorf("%s exceeds maximum length %d", kind, MaxIDLength)
	}
	for _, r := range id {
		if !isAllowedIDRune(r) {
			return fmt.Errorf("invalid %s", kind)
		}
	}
	return nil
}

func isAllowedIDRune(r rune) bool {
	switch {
	case unicode.IsLetter(r), unicode.IsDigit(r):
		return true
	case r == '-' || r == '_' || r == '.' || r == ':':
		return true
	default:
		return false
	}
}
