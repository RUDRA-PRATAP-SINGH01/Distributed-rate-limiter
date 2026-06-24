// Prometheus metrics for the rate limiter fleet.
//
// Labels are intentionally low-cardinality (handler + allowed) — never per-user —
// because unbounded label values would OOM the Prometheus TSDB under real traffic.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	RequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rate_limiter_requests_total",
			Help: "Total number of requests processed",
		},
		[]string{"handler", "allowed"},
	)

	RequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "rate_limiter_requests_duration_seconds",
			Help:    "Request latency in seconds",
			Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		},
		[]string{"handler"},
	)

	RedisDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "rate_limiter_redis_duration_seconds",
			Help:    "Redis operation latency in seconds",
			Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		},
	)

	CacheHits = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "rate_limiter_sidecar_cache_hits_total",
			Help: "Number of local cache hits",
		},
	)
	CacheMisses = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "rate_limiter_sidecar_cache_misses_total",
			Help: "Number of local cache misses",
		},
	)

	IdempotencyClaims = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "idempotency_claims_total",
			Help: "Idempotency claim outcomes",
		},
		[]string{"result"},
	)

	IdempotencyCompletes = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "idempotency_completes_total",
			Help: "Idempotency records completed with stored responses",
		},
	)

	IdempotencyRedisDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "idempotency_redis_duration_seconds",
			Help:    "Idempotency Redis Lua operation latency",
			Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		},
	)
)

func RecordRequest(handler string, allowed bool) {
	allowedStr := "false"
	if allowed {
		allowedStr = "true"
	}
	RequestsTotal.WithLabelValues(handler, allowedStr).Inc()
}

func RecordRequestDuration(handler string, duration float64) {
	RequestDuration.WithLabelValues(handler).Observe(duration)
}

func RecordRedisDuration(duration float64) {
	RedisDuration.Observe(duration)
}

func RecordCacheHit() {
	CacheHits.Inc()
}

func RecordCacheMiss() {
	CacheMisses.Inc()
}

func RecordIdempotencyClaim(result string) {
	IdempotencyClaims.WithLabelValues(result).Inc()
}

func RecordIdempotencyComplete() {
	IdempotencyCompletes.Inc()
}

func RecordIdempotencyRedisDuration(duration float64) {
	IdempotencyRedisDuration.Observe(duration)
}
