package main

import (
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/telemetry"
)

// LimiterHTTPConfig bounds outbound calls to the central limiter.
// A synchronous rate-limit check must fail fast when the limiter is unavailable.
type LimiterHTTPConfig struct {
	ClientTimeout        time.Duration
	DialTimeout          time.Duration
	ResponseHeaderTimeout time.Duration
	TLSHandshakeTimeout  time.Duration
}

func defaultLimiterHTTPConfig() LimiterHTTPConfig {
	return LimiterHTTPConfig{
		ClientTimeout:         1500 * time.Millisecond,
		DialTimeout:           500 * time.Millisecond,
		ResponseHeaderTimeout: 1000 * time.Millisecond,
		TLSHandshakeTimeout:   1000 * time.Millisecond,
	}
}

func loadLimiterHTTPConfigFromEnv() LimiterHTTPConfig {
	cfg := defaultLimiterHTTPConfig()
	cfg.ClientTimeout = loadPositiveDurationMS("SIDECAR_LIMITER_HTTP_TIMEOUT_MS", cfg.ClientTimeout)
	cfg.DialTimeout = loadPositiveDurationMS("SIDECAR_LIMITER_DIAL_TIMEOUT_MS", cfg.DialTimeout)
	cfg.ResponseHeaderTimeout = loadPositiveDurationMS("SIDECAR_LIMITER_HEADER_TIMEOUT_MS", cfg.ResponseHeaderTimeout)
	cfg.TLSHandshakeTimeout = loadPositiveDurationMS("SIDECAR_LIMITER_TLS_TIMEOUT_MS", cfg.TLSHandshakeTimeout)
	return cfg
}

func loadPositiveDurationMS(key string, current time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return current
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms <= 0 {
		return current
	}
	return time.Duration(ms) * time.Millisecond
}

func newLimiterHTTPClient(cfg LimiterHTTPConfig) *http.Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   cfg.DialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   cfg.TLSHandshakeTimeout,
		ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
	}
	return &http.Client{
		Timeout:   cfg.ClientTimeout,
		Transport: telemetry.NewHTTPTransport(transport),
	}
}
