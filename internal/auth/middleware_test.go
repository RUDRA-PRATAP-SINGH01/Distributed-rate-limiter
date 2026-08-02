package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecureCompare(t *testing.T) {
	const expected = "super-secret-production-key-12345"

	tests := []struct {
		name     string
		input    string
		expected string
		want     bool
	}{
		{"exact match", expected, expected, true},
		{"same length wrong byte at start", "xuper-secret-production-key-12345", expected, false},
		{"same length wrong byte at end", "super-secret-production-key-12346", expected, false},
		{"same length wrong byte in middle", "super-secret-producXion-key-12345", expected, false},
		{"single char", "s", expected, false},
		{"prefix only (shorter)", "super-secret", expected, false},
		{"suffix only (shorter)", "key-12345", expected, false},
		{"longer key with matching prefix", expected + "-extra-payload", expected, false},
		{"very long key (512 chars)", strings.Repeat("A", 512), expected, false},
		{"empty input key", "", expected, false},
		{"empty expected key", expected, "", false},
		{"both empty", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SecureCompare(tt.input, tt.expected)
			if got != tt.want {
				t.Errorf("SecureCompare(%q, %q) = %v, want %v", tt.input, tt.expected, got, tt.want)
			}
		})
	}
}

func TestRequireAPIKey(t *testing.T) {
	const validKey = "valid-api-key-test-value"

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	})

	tests := []struct {
		name           string
		expectedKey    string
		apiKeyHeader   string
		internalHeader string
		wantStatus     int
	}{
		{
			name:         "exact match on X-API-Key",
			expectedKey:  validKey,
			apiKeyHeader: validKey,
			wantStatus:   http.StatusOK,
		},
		{
			name:           "exact match on X-Internal-API-Key",
			expectedKey:    validKey,
			internalHeader: validKey,
			wantStatus:     http.StatusOK,
		},
		{
			name:           "X-API-Key takes precedence over X-Internal-API-Key",
			expectedKey:    validKey,
			apiKeyHeader:   validKey,
			internalHeader: "wrong-internal-key",
			wantStatus:     http.StatusOK,
		},
		{
			name:         "short wrong key",
			expectedKey:  validKey,
			apiKeyHeader: "v",
			wantStatus:   http.StatusUnauthorized,
		},
		{
			name:         "long wrong key",
			expectedKey:  validKey,
			apiKeyHeader: strings.Repeat("x", 256),
			wantStatus:   http.StatusUnauthorized,
		},
		{
			name:         "same length wrong key",
			expectedKey:  validKey,
			apiKeyHeader: "valid-api-key-test-valuX",
			wantStatus:   http.StatusUnauthorized,
		},
		{
			name:           "wrong X-API-Key is not rescued by valid X-Internal-API-Key",
			expectedKey:    validKey,
			apiKeyHeader:   "wrong",
			internalHeader: validKey,
			wantStatus:     http.StatusUnauthorized,
		},
		{
			name:        "missing key header",
			expectedKey: validKey,
			wantStatus:  http.StatusUnauthorized,
		},
		{
			name:        "empty expected key disables auth (bypass)",
			expectedKey: "",
			wantStatus:  http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := RequireAPIKey(tt.expectedKey, okHandler)

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.apiKeyHeader != "" {
				req.Header.Set(APIKeyHeader, tt.apiKeyHeader)
			}
			if tt.internalHeader != "" {
				req.Header.Set(InternalAPIKeyHeader, tt.internalHeader)
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("RequireAPIKey status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}
