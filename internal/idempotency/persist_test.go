package idempotency

import (
	"net/http"
	"testing"

	redisclient "github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/redis"
)

func TestPersistAsComplete(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status int
		want   bool
	}{
		{0, false},
		{http.StatusOK, true},
		{http.StatusCreated, true},
		{http.StatusBadRequest, true},
		{http.StatusNotFound, true},
		{http.StatusTooManyRequests, true},
		{http.StatusRequestTimeout, false},
		{http.StatusTooEarly, false},
		{http.StatusInternalServerError, false},
		{http.StatusBadGateway, false},
		{http.StatusServiceUnavailable, false},
		{http.StatusGatewayTimeout, false},
	}
	for _, tc := range cases {
		if got := PersistAsComplete(tc.status); got != tc.want {
			t.Errorf("PersistAsComplete(%d) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestEvalKeysShareClusterSlot(t *testing.T) {
	t.Parallel()
	scope := BuildScope("tenant-a", "user-1")
	keys := evalKeys(scope, "idem-key-1")
	if len(keys) != 2 {
		t.Fatalf("expected meta+body keys, got %d", len(keys))
	}
	if !redisclient.SameClusterSlot(keys...) {
		t.Fatalf("idempotency EVAL keys must share a hash tag, got %v", keys)
	}
	if redisclient.ClusterSlotTag(keys[0]) != redisclient.SanitizeHashTag(scope) {
		t.Fatalf("tag %q want %q", redisclient.ClusterSlotTag(keys[0]), scope)
	}
	injected := evalKeys("evil}{other", "k")
	if !redisclient.SameClusterSlot(injected...) {
		t.Fatalf("sanitized scope must still co-locate keys, got %v", injected)
	}
	if redisclient.ClusterSlotTag(injected[0]) == "evil" {
		t.Fatal("user-controlled braces must not inject a hash tag")
	}
}
