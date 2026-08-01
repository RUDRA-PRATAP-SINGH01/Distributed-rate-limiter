package audit

import (
	"os"
	"strconv"
	"time"
)

// Config controls audit retention and capacity.
type Config struct {
	Enabled   bool
	Retention time.Duration
	MaxEvents int64
	Async     bool
	QueueSize int
	Workers   int
}

func DefaultConfig() Config {
	return Config{
		Enabled:   true,
		Retention: 7 * 24 * time.Hour,
		MaxEvents: 100_000,
		Async:     true,
		QueueSize: 4096,
		Workers:   4,
	}
}

func LoadConfigFromEnv() Config {
	cfg := DefaultConfig()
	if os.Getenv("ENABLE_AUDIT_TRAIL") == "false" {
		cfg.Enabled = false
	}
	if raw := os.Getenv("AUDIT_RETENTION_HOURS"); raw != "" {
		if h, err := strconv.Atoi(raw); err == nil && h > 0 {
			cfg.Retention = time.Duration(h) * time.Hour
		}
	}
	if raw := os.Getenv("AUDIT_MAX_EVENTS"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			cfg.MaxEvents = n
		}
	}
	if os.Getenv("AUDIT_ASYNC") == "false" {
		cfg.Async = false
	}
	if raw := os.Getenv("AUDIT_QUEUE_SIZE"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			cfg.QueueSize = n
		}
	}
	if raw := os.Getenv("AUDIT_WORKERS"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			cfg.Workers = n
		}
	}
	return cfg
}
