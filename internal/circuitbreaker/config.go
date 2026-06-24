package circuitbreaker

import (
	"os"
	"strconv"
)

// Config tunes trip thresholds and half-open probe budget.
type Config struct {
	FailureRateThreshold    float64
	MinSamples              int64
	ConsecutiveFailures     int64
	LatencyThresholdMs      int64
	TimeoutRateThreshold    float64
	OpenCooldownMs          int64
	HalfOpenMaxProbes       int64
	HalfOpenSuccessRequired int64
	EMAAlpha                float64
	FailOpen                bool // when true, Redis errors allow traffic (dangerous)
}

func DefaultConfig() Config {
	return Config{
		FailureRateThreshold:    0.5,
		MinSamples:              10,
		ConsecutiveFailures:     5,
		LatencyThresholdMs:      500,
		TimeoutRateThreshold:    0.3,
		OpenCooldownMs:          30_000,
		HalfOpenMaxProbes:       3,
		HalfOpenSuccessRequired: 2,
		EMAAlpha:                0.2,
	}
}

func LoadConfigFromEnv() Config {
	cfg := DefaultConfig()
	if v := parseFloatEnv("CB_FAILURE_RATE", cfg.FailureRateThreshold); v > 0 {
		cfg.FailureRateThreshold = v
	}
	if v := parseIntEnv("CB_MIN_SAMPLES", int(cfg.MinSamples)); v > 0 {
		cfg.MinSamples = int64(v)
	}
	if v := parseIntEnv("CB_CONSECUTIVE_FAILURES", int(cfg.ConsecutiveFailures)); v > 0 {
		cfg.ConsecutiveFailures = int64(v)
	}
	if v := parseIntEnv("CB_LATENCY_THRESHOLD_MS", int(cfg.LatencyThresholdMs)); v > 0 {
		cfg.LatencyThresholdMs = int64(v)
	}
	if v := parseFloatEnv("CB_TIMEOUT_RATE", cfg.TimeoutRateThreshold); v > 0 {
		cfg.TimeoutRateThreshold = v
	}
	if v := parseIntEnv("CB_OPEN_COOLDOWN_MS", int(cfg.OpenCooldownMs)); v > 0 {
		cfg.OpenCooldownMs = int64(v)
	}
	if v := parseIntEnv("CB_HALF_OPEN_MAX_PROBES", int(cfg.HalfOpenMaxProbes)); v > 0 {
		cfg.HalfOpenMaxProbes = int64(v)
	}
	if v := parseIntEnv("CB_HALF_OPEN_SUCCESS_REQUIRED", int(cfg.HalfOpenSuccessRequired)); v > 0 {
		cfg.HalfOpenSuccessRequired = int64(v)
	}
	if v := parseFloatEnv("CB_EMA_ALPHA", cfg.EMAAlpha); v > 0 && v <= 1 {
		cfg.EMAAlpha = v
	}
	if os.Getenv("CIRCUIT_FAIL_OPEN") == "true" {
		cfg.FailOpen = true
	}
	return cfg
}

func parseFloatEnv(key string, def float64) float64 {
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return def
	}
	return v
}

func parseIntEnv(key string, def int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return v
}
