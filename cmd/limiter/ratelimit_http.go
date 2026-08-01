package main

import (
	"fmt"
	"math"
	"net/http"
	"time"
)

// retryAfterForCheck derives a conservative Retry-After from the active algorithm.
// Token bucket: time to refill one token. Sliding window: full window length —
// only a fallback, since a sliding log has no shared reset instant and this
// over-states the wait. Prefer retryAfterHeader with a measured duration.
func retryAfterForCheck(cfg Config) string {
	switch cfg.Algorithm {
	case "sliding":
		return fmt.Sprintf("%d", cfg.WindowSec)
	default:
		seconds := int(math.Ceil(1.0 / cfg.RefillRate))
		if seconds < 1 {
			seconds = 1
		}
		return fmt.Sprintf("%d", seconds)
	}
}

// retryAfterHeader prefers the limiter's measured wait over the config-derived
// estimate. RFC 9110 requires whole seconds, so sub-second waits round up to 1
// rather than inviting an immediate retry.
func retryAfterHeader(cfg Config, measured time.Duration) string {
	if measured <= 0 {
		return retryAfterForCheck(cfg)
	}
	seconds := int(math.Ceil(measured.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("%d", seconds)
}

// retryAfterForHierarchical uses the slowest refill rate across levels.
// When four buckets stack, the client should wait for the tightest refill curve.
func retryAfterForHierarchical(cfg Config) string {
	minRate := cfg.EndpointRefillRate
	if cfg.UserRefillRate < minRate {
		minRate = cfg.UserRefillRate
	}
	if cfg.TenantRefillRate < minRate {
		minRate = cfg.TenantRefillRate
	}
	if cfg.GlobalRefillRate < minRate {
		minRate = cfg.GlobalRefillRate
	}
	seconds := int(math.Ceil(1.0 / minRate))
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("%d", seconds)
}

func rateLimitLimitHeader(cfg Config) string {
	return fmt.Sprintf("%d", cfg.Capacity)
}

func setRateLimitHeaders(w http.ResponseWriter, limit string, remaining int) {
	w.Header().Set("X-RateLimit-Limit", limit)
	w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
}
