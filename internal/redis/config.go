package redis

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Mode selects standalone Redis or Sentinel-managed failover.
type Mode string

const (
	ModeStandalone Mode = "standalone"
	ModeSentinel   Mode = "sentinel"
)

// Config drives client factory and health reporting.
type Config struct {
	Mode             Mode
	Addr             string
	Password         string
	MasterName       string
	SentinelAddrs    []string
	SentinelPassword string
	DB               int
	PoolSize         int
	MinIdleConns     int
	DialTimeout      time.Duration
	ReadTimeout      time.Duration
	WriteTimeout     time.Duration
	PoolTimeout      time.Duration
	MaxRetries       int
	DialerRetries    int
}

func DefaultConfig() Config {
	t := defaultClientTimeouts()
	return Config{
		Mode:          ModeStandalone,
		Addr:          "localhost:6379",
		MasterName:    "mymaster",
		PoolSize:      100,
		MinIdleConns:  10,
		DialTimeout:   t.DialTimeout,
		ReadTimeout:   t.ReadTimeout,
		WriteTimeout:  t.WriteTimeout,
		PoolTimeout:   t.PoolTimeout,
		MaxRetries:    t.MaxRetries,
		DialerRetries: t.DialerRetries,
	}
}

func LoadConfigFromEnv() Config {
	cfg := DefaultConfig()
	cfg.Addr = getEnv("REDIS_ADDR", cfg.Addr)
	cfg.Password = getEnv("REDIS_PASSWORD", "")
	cfg.MasterName = getEnv("REDIS_MASTER_NAME", cfg.MasterName)
	cfg.SentinelPassword = getEnv("REDIS_SENTINEL_PASSWORD", cfg.Password)

	if raw := getEnv("REDIS_MODE", string(ModeStandalone)); raw != "" {
		cfg.Mode = Mode(strings.ToLower(strings.TrimSpace(raw)))
	}
	if raw := getEnv("REDIS_SENTINEL_ADDRS", ""); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				cfg.SentinelAddrs = append(cfg.SentinelAddrs, part)
			}
		}
	}
	if v := getEnv("REDIS_DB", "0"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.DB = n
		}
	}
	cfg.DialTimeout = loadDurationEnvMS("REDIS_DIAL_TIMEOUT_MS", cfg.DialTimeout)
	cfg.ReadTimeout = loadDurationEnvMS("REDIS_READ_TIMEOUT_MS", cfg.ReadTimeout)
	cfg.WriteTimeout = loadDurationEnvMS("REDIS_WRITE_TIMEOUT_MS", cfg.WriteTimeout)
	cfg.PoolTimeout = loadDurationEnvMS("REDIS_POOL_TIMEOUT_MS", cfg.PoolTimeout)
	cfg.DialerRetries = loadPositiveIntEnv("REDIS_DIALER_RETRIES", cfg.DialerRetries)
	cfg.MaxRetries = loadNonNegativeIntEnv("REDIS_MAX_RETRIES", cfg.MaxRetries)
	return cfg
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
