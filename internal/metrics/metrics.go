package metrics

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

var (
    // Total requests received
    RequestsTotal = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "rate_limiter_requests_total",
            Help: "Total number of requests processed",
        },
        []string{"user", "allowed"},
    )

    // Request duration histogram (sub-10ms buckets for rate-limiter latency)
    RequestDuration = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "rate_limiter_requests_duration_seconds",
            Help:    "Request latency in seconds",
            Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
        },
        []string{"user"},
    )

    // Redis Lua script operation duration
    RedisDuration = promauto.NewHistogram(
        prometheus.HistogramOpts{
            Name:    "rate_limiter_redis_duration_seconds",
            Help:    "Redis operation latency in seconds",
            Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
        },
    )

    // Cache hit ratio (sidecar)
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
)

func RecordRequest(user string, allowed bool) {
    allowedStr := "false"
    if allowed {
        allowedStr = "true"
    }
    RequestsTotal.WithLabelValues(user, allowedStr).Inc()
}

func RecordRequestDuration(user string, duration float64) {
    RequestDuration.WithLabelValues(user).Observe(duration)
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