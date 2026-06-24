package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/circuitbreaker"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/telemetry"
	"go.opentelemetry.io/otel/trace"
)

func checkRedisCircuit(ctx context.Context, w http.ResponseWriter, span trace.Span) bool {
	if redisCircuit == nil {
		return true
	}
	allow, err := redisCircuit.Allow(ctx, circuitbreaker.TargetRedis)
	if err != nil {
		return true
	}
	if allow.Allowed {
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	json.NewEncoder(w).Encode(map[string]string{
		"error":         "Rate limiter unavailable",
		"circuit_state": string(allow.State),
	})
	telemetry.SetHTTPStatus(span, http.StatusServiceUnavailable)
	return false
}

func recordRedisCircuit(ctx context.Context, err error, start time.Time) {
	if redisCircuit == nil {
		return
	}
	latency := time.Since(start)
	input := circuitbreaker.ClassifyError(err, latency, redisCircuit.Config().LatencyThresholdMs)
	_ = redisCircuit.Record(ctx, circuitbreaker.TargetRedis, input)
}
