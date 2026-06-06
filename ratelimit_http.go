package main

import (
	"fmt"
	"math"
	"net/http"
)

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

func retryAfterForHierarchical(cfg Config) string {
	// Use the slowest refill among levels (usually endpoint) as a conservative hint.
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
