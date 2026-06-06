package identity

import (
	"fmt"
	"net/http"
	"strings"
)

const UserIDHeader = "X-User-ID"

// ResolveUserID returns a trusted user identifier.
// Production: only X-User-ID (set by auth gateway or sidecar).
// Dev/demo: query ?user_id= allowed when allowQuery is true.
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
