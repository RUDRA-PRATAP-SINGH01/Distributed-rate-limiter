package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/circuitbreaker"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/identity"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestLimiterHTTPConfigDefaults(t *testing.T) {
	cfg := defaultLimiterHTTPConfig()
	if cfg.ClientTimeout != 1500*time.Millisecond {
		t.Fatalf("ClientTimeout=%v", cfg.ClientTimeout)
	}
	if cfg.DialTimeout != 500*time.Millisecond || cfg.ResponseHeaderTimeout != time.Second {
		t.Fatalf("transport timeouts: %+v", cfg)
	}
}

func TestLimiterHTTPConfigEnv(t *testing.T) {
	t.Setenv("SIDECAR_LIMITER_HTTP_TIMEOUT_MS", "2000")
	t.Setenv("SIDECAR_LIMITER_DIAL_TIMEOUT_MS", "400")
	t.Setenv("SIDECAR_LIMITER_HEADER_TIMEOUT_MS", "800")
	cfg := loadLimiterHTTPConfigFromEnv()
	if cfg.ClientTimeout != 2*time.Second || cfg.DialTimeout != 400*time.Millisecond || cfg.ResponseHeaderTimeout != 800*time.Millisecond {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
}

func TestLimiterHTTPConfigInvalidEnvKeepsDefault(t *testing.T) {
	t.Setenv("SIDECAR_LIMITER_HTTP_TIMEOUT_MS", "bad")
	cfg := loadLimiterHTTPConfigFromEnv()
	if cfg.ClientTimeout != 1500*time.Millisecond {
		t.Fatalf("expected default client timeout, got %v", cfg.ClientTimeout)
	}
}

func TestLimiterHTTPClientConnectionRefusedBounded(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	cfg := LimiterHTTPConfig{
		ClientTimeout:         800 * time.Millisecond,
		DialTimeout:           200 * time.Millisecond,
		ResponseHeaderTimeout: 400 * time.Millisecond,
		TLSHandshakeTimeout:   400 * time.Millisecond,
	}
	client := newLimiterHTTPClient(cfg)

	start := time.Now()
	_, err = client.Get("http://" + addr + "/check")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected connection error")
	}
	if elapsed > cfg.ClientTimeout+200*time.Millisecond {
		t.Fatalf("refused dial took %v, exceeds budget", elapsed)
	}
}

func TestLimiterHTTPClientDelayedHeadersTimesOut(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	defer close(release)

	cfg := LimiterHTTPConfig{
		ClientTimeout:         600 * time.Millisecond,
		DialTimeout:           200 * time.Millisecond,
		ResponseHeaderTimeout: 300 * time.Millisecond,
		TLSHandshakeTimeout:   300 * time.Millisecond,
	}
	client := newLimiterHTTPClient(cfg)

	start := time.Now()
	_, err := client.Get(srv.URL)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > cfg.ClientTimeout+250*time.Millisecond {
		t.Fatalf("delayed headers took %v, exceeds budget", elapsed)
	}
}

func TestCheckRateLimit429KeepsCBClosedAndSpanUnset(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 30*time.Millisecond, false)
	defer cleanup()

	fixture.limiterHandler.allowed = false
	fixture.limiterHandler.statusCode = http.StatusTooManyRequests

	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))

	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req.Header.Set(identity.UserIDHeader, "user-429-http")
	rr := httptest.NewRecorder()
	fixture.sidecar.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rr.Code)
	}
	state, err := fixture.sidecar.limiterCircuit.GetState(context.Background(), circuitbreaker.TargetCentralLimiter)
	if err != nil {
		t.Fatal(err)
	}
	if state.State != circuitbreaker.StateClosed {
		t.Fatalf("expected closed CB on 429, got %s", state.State)
	}

	var clientSpan sdktrace.ReadOnlySpan
	for _, sp := range sr.Ended() {
		if sp.Name() == "HTTP GET" {
			clientSpan = sp
			break
		}
	}
	if clientSpan == nil {
		t.Fatal("expected HTTP GET client span")
	}
	if clientSpan.Status().Code != codes.Unset {
		t.Fatalf("expected unset span status for 429, got %v", clientSpan.Status().Code)
	}
}

func TestCheckRateLimit5xxRecordsCBFailure(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 30*time.Millisecond, false)
	defer cleanup()

	fixture.limiterHandler.statusCode = http.StatusInternalServerError

	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req.Header.Set(identity.UserIDHeader, "user-500")
	rr := httptest.NewRecorder()
	fixture.sidecar.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rr.Code)
	}
	state, _ := fixture.sidecar.limiterCircuit.GetState(context.Background(), circuitbreaker.TargetCentralLimiter)
	if state.FailureCount < 1 {
		t.Fatalf("expected failure recorded, state=%+v", state)
	}
}

func TestLimiterHTTPTracePropagation(t *testing.T) {
	var gotTraceparent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceparent = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"allowed":true}`))
	}))
	defer srv.Close()

	client := newLimiterHTTPClient(defaultLimiterHTTPConfig())

	otel.SetTextMapPropagator(propagation.TraceContext{})
	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))

	ctx, span := otel.Tracer("test").Start(context.Background(), "parent")
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := client.Do(req)
	span.End()
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if gotTraceparent == "" {
		t.Fatal("traceparent header not propagated to limiter")
	}
}
