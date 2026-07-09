package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/circuitbreaker"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/logging"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type testFixture struct {
	sidecar         *Sidecar
	mr              *miniredis.Miniredis
	rdb             redis.UniversalClient
	upstreamSrv     *httptest.Server
	limiterSrv      *httptest.Server
	limiterHandler  *mockLimiterHandler
	upstreamHandler *mockUpstreamHandler
	upstreamURL     string
	limiterURL      string
}

type mockLimiterHandler struct {
	mu         sync.Mutex
	allowed    bool
	limit      int
	remaining  int
	retryAfter string
	statusCode int
	bodyJSON   string // if set, overrides standard responses

	// Diagnostics
	callCount int
	lastUser  string
	lastPath  string
	lastKey   string

	// Concurrency control
	blockChan chan struct{}
}

func (h *mockLimiterHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.callCount++
	h.lastUser = r.Header.Get("X-User-ID")
	h.lastPath = r.URL.Path
	h.lastKey = r.Header.Get("X-API-Key")
	if h.lastKey == "" {
		h.lastKey = r.Header.Get("X-Internal-API-Key")
	}

	allowed := h.allowed
	limit := h.limit
	remaining := h.remaining
	retryAfter := h.retryAfter
	statusCode := h.statusCode
	bodyJSON := h.bodyJSON
	blockChan := h.blockChan
	h.mu.Unlock()

	if blockChan != nil {
		<-blockChan
	}

	w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
	w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
	if retryAfter != "" {
		w.Header().Set("Retry-After", retryAfter)
	}

	status := statusCode
	if status == 0 {
		status = http.StatusOK
	}

	if bodyJSON != "" {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(bodyJSON))
		return
	}

	if !allowed && status == http.StatusOK {
		status = http.StatusTooManyRequests
	}

	w.WriteHeader(status)
	if status == http.StatusOK {
		_, _ = w.Write([]byte(`{"allowed":true}`))
	} else if status == http.StatusTooManyRequests {
		_, _ = w.Write([]byte(`{"allowed":false}`))
	} else {
		_, _ = w.Write([]byte(`{"error":"limiter failure"}`))
	}
}

type mockUpstreamHandler struct {
	mu         sync.Mutex
	statusCode int
	body       string
	callCount  int
	blockChan  chan struct{}
	customFn   func(w http.ResponseWriter, r *http.Request)
}

func (h *mockUpstreamHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.callCount++
	statusCode := h.statusCode
	body := h.body
	blockChan := h.blockChan
	customFn := h.customFn
	h.mu.Unlock()

	if blockChan != nil {
		<-blockChan
	}

	if customFn != nil {
		customFn(w, r)
		return
	}

	status := statusCode
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if body != "" {
		_, _ = w.Write([]byte(body))
	} else {
		_, _ = w.Write([]byte("upstream-ok"))
	}
}

func newTestFixture(t *testing.T, failOpen bool, ttl time.Duration, useHierarchical bool) (*testFixture, func()) {
	t.Helper()
	logging.Init()

	// Upstream Server
	upstreamHandler := &mockUpstreamHandler{}
	upstreamSrv := httptest.NewServer(upstreamHandler)

	// Limiter Server
	limiterHandler := &mockLimiterHandler{
		allowed:   true,
		limit:     10,
		remaining: 9,
	}
	limiterSrv := httptest.NewServer(limiterHandler)

	// Redis Mock
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	// Sidecar Instance
	sidecar := NewSidecar(
		upstreamSrv.URL,
		limiterSrv.URL,
		"test-internal-key",
		"test-metrics-key",
		ttl,
		failOpen,
		10,
		useHierarchical,
		true, // allowQueryUserID
		[]string{"/"},
	)

	// Set Circuit Breaker
	cbCfg := circuitbreaker.DefaultConfig()
	cbCfg.OpenCooldownMs = 50 // Short open period for tests
	cbCfg.MinSamples = 2
	cbCfg.ConsecutiveFailures = 2
	cbCfg.FailureRateThreshold = 0.5
	store := circuitbreaker.NewRedisStore(rdb, cbCfg)
	sidecar.SetLimiterCircuit(circuitbreaker.NewBreaker(store))

	fixture := &testFixture{
		sidecar:         sidecar,
		mr:              mr,
		rdb:             rdb,
		upstreamSrv:     upstreamSrv,
		limiterSrv:      limiterSrv,
		limiterHandler:  limiterHandler,
		upstreamHandler: upstreamHandler,
		upstreamURL:     upstreamSrv.URL,
		limiterURL:      limiterSrv.URL,
	}

	cleanup := func() {
		limiterSrv.Close()
		upstreamSrv.Close()
		_ = rdb.Close()
		mr.Close()
	}

	return fixture, cleanup
}
