package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/logging"
)

// Config holds all runtime tuning knobs. Everything is env-driven so the same binary
// runs locally, in Docker, and on Railway without recompilation.
type Config struct {
	Port          int
	RedisAddr     string
	RedisPassword string
	Algorithm     string
	Capacity      int
	RefillRate    float64
	WindowSec     int

	EnableHierarchical bool
	EnableAdminAPI     bool
	AdminPort          int
	AdminAPIKey        string
	OverrideCacheTTLMs int

	// Security
	InternalAPIKey     string
	MetricsAPIKey      string
	MetricsRequireAuth bool
	AllowQueryUserID   bool
	StrictConfig       bool
	StrictSecurity     bool
	TLSCertFile        string
	TLSKeyFile         string

	// Hierarchical limits — each level can independently cap traffic (SaaS-style quotas).
	GlobalCapacity     int
	GlobalRefillRate   float64
	TenantCapacity     int
	TenantRefillRate   float64
	UserCapacity       int
	UserRefillRate     float64
	EndpointCapacity   int
	EndpointRefillRate float64
}

func (c Config) AdminAddr() string {
	return fmt.Sprintf(":%d", c.AdminPort)
}

func (c Config) MetricsAuthKey() string {
	if c.MetricsAPIKey != "" {
		return c.MetricsAPIKey
	}
	return c.InternalAPIKey
}

func LoadConfig() Config {
	strict := getEnv("STRICT_CONFIG", "false") == "true"
	internalKey := getEnv("INTERNAL_API_KEY", "")

	cfg := Config{
		Port:               mustParseIntEnv("PORT", "8080", strict),
		RedisAddr:          getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:      getEnv("REDIS_PASSWORD", ""),
		Algorithm:          getEnv("ALGORITHM", "token"),
		Capacity:           mustParseIntEnv("CAPACITY", "10", strict),
		RefillRate:         mustParseFloatEnv("REFILL_RATE", "1.0", strict),
		WindowSec:          mustParseIntEnv("WINDOW_SEC", "60", strict),
		EnableHierarchical: getEnv("ENABLE_HIERARCHICAL", "true") == "true",
		EnableAdminAPI:     getEnv("ENABLE_ADMIN_API", "true") == "true",
		AdminPort:          mustParseIntEnv("ADMIN_PORT", "8082", strict),
		// Default is a dev placeholder only — override in any shared or production environment.
		AdminAPIKey:        getEnv("ADMIN_API_KEY", "dev-key-change-in-prod"),
		OverrideCacheTTLMs: mustParseIntEnv("OVERRIDE_CACHE_TTL_MS", "5000", strict),

		InternalAPIKey:     internalKey,
		MetricsAPIKey:      getEnv("METRICS_API_KEY", ""),
		MetricsRequireAuth: getEnv("METRICS_REQUIRE_AUTH", "false") == "true",
		AllowQueryUserID:   getEnv("ALLOW_QUERY_USER_ID", "false") == "true",
		StrictConfig:       strict,
		StrictSecurity:     getEnv("STRICT_SECURITY", "false") == "true",
		TLSCertFile:        getEnv("TLS_CERT_FILE", ""),
		TLSKeyFile:         getEnv("TLS_KEY_FILE", ""),
		GlobalCapacity:     mustParseIntEnv("GLOBAL_CAPACITY", "1000000", strict),
		GlobalRefillRate:   mustParseFloatEnv("GLOBAL_REFILL_RATE", "10000.0", strict),
		TenantCapacity:     mustParseIntEnv("TENANT_CAPACITY", "100000", strict),
		TenantRefillRate:   mustParseFloatEnv("TENANT_REFILL_RATE", "1000.0", strict),
		UserCapacity:       mustParseIntEnv("USER_CAPACITY", "100", strict),
		UserRefillRate:     mustParseFloatEnv("USER_REFILL_RATE", "1.0", strict),
		EndpointCapacity:   mustParseIntEnv("ENDPOINT_CAPACITY", "10", strict),
		EndpointRefillRate: mustParseFloatEnv("ENDPOINT_REFILL_RATE", "0.5", strict),
	}

	if cfg.StrictSecurity && cfg.InternalAPIKey == "" {
		logging.Fatal("STRICT_SECURITY=true requires INTERNAL_API_KEY")
	}
	if cfg.StrictSecurity && cfg.EnableAdminAPI && (cfg.AdminAPIKey == "" || cfg.AdminAPIKey == "dev-key-change-in-prod") {
		logging.Fatal("STRICT_SECURITY=true requires a non-default ADMIN_API_KEY when ADMIN_API is enabled")
	}
	if cfg.InternalAPIKey == "" {
		logging.Warn(context.Background(), "INTERNAL_API_KEY is not set — /check endpoints are reachable without authentication",
			"component", "limiter",
			"security_dev_mode", true,
		)
	}
	if cfg.EnableAdminAPI && (cfg.AdminAPIKey == "" || cfg.AdminAPIKey == "dev-key-change-in-prod") {
		logging.Warn(context.Background(), "ADMIN_API_KEY is using default dev placeholder — admin endpoints are insecure",
			"component", "limiter",
			"security_dev_mode", true,
		)
	}
	if cfg.TLSCertFile != "" || cfg.TLSKeyFile != "" {
		if cfg.TLSCertFile == "" || cfg.TLSKeyFile == "" {
			logging.Fatal("TLS_CERT_FILE and TLS_KEY_FILE must both be set to enable TLS")
		}
	}

	return cfg
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

// mustParseIntEnv rejects garbage like CAPACITY=abc. In strict mode we fail startup
// instead of silently running with capacity 0 (which would deny everyone or break Lua).
func mustParseIntEnv(key, defaultVal string, strict bool) int {
	raw := getEnv(key, defaultVal)
	value, err := strconv.Atoi(raw)
	defaultInt, _ := strconv.Atoi(defaultVal)
	if err != nil || value <= 0 {
		msg := fmt.Sprintf("invalid %s=%q", key, raw)
		if strict {
			logging.Fatal(msg)
		}
		if raw != defaultVal {
			logging.Warn(context.Background(), msg+", using default "+defaultVal,
				"component", "limiter",
				"config_key", key,
			)
		}
		return defaultInt
	}
	return value
}

func mustParseFloatEnv(key, defaultVal string, strict bool) float64 {
	raw := getEnv(key, defaultVal)
	value, err := strconv.ParseFloat(raw, 64)
	defaultFloat, _ := strconv.ParseFloat(defaultVal, 64)
	if err != nil || value <= 0 {
		msg := fmt.Sprintf("invalid %s=%q", key, raw)
		if strict {
			logging.Fatal(msg)
		}
		if raw != defaultVal {
			logging.Warn(context.Background(), msg+", using default "+defaultVal,
				"component", "limiter",
				"config_key", key,
			)
		}
		return defaultFloat
	}
	return value
}
