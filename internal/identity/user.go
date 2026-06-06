// Package identity resolves who is consuming quota on a request.
//
// Production traffic should arrive with X-User-ID set by an auth gateway (JWT validated
// upstream). Query-string user_id is opt-in for local demos only.
package identity

import (
	"fmt"
	"net/http"
	"strings"
)

const UserIDHeader = "X-User-ID"

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
