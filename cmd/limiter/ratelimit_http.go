package main

import (
	"fmt"
	"math"
	"net/http"
)

// retryAfterForCheck derives a conservative Retry-After from the active algorithm.
// Token bucket: time to refill one token. Sliding window: full window length.
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

// Static header helper when overrides are not in play (legacy / docs).
func hierarchicalRateLimitLimitHeader(cfg Config) string {
	limit := cfg.GlobalCapacity
	if cfg.TenantCapacity < limit {
		limit = cfg.TenantCapacity
	}
	if cfg.UserCapacity < limit {
		limit = cfg.UserCapacity
	}
	if cfg.EndpointCapacity < limit {
		limit = cfg.EndpointCapacity
	}
	return fmt.Sprintf("%d", limit)
}

func setRateLimitHeaders(w http.ResponseWriter, limit string, remaining int) {
	w.Header().Set("X-RateLimit-Limit", limit)
	w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
}
