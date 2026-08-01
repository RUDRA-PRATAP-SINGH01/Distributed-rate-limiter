package redis

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// ClientTimeouts are the effective go-redis timeout/retry settings applied by New().
type ClientTimeouts struct {
	DialTimeout     time.Duration
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	PoolTimeout     time.Duration
	MaxRetries      int // command-level retries (0 = disabled)
	DialerRetries   int
	MinRetryBackoff time.Duration
	MaxRetryBackoff time.Duration
}

// Default timeouts target a deterministic outage budget:
//
//	pool wait (1s) + one dial attempt (500ms) + read/write (1s) per command,
//
// with no command-level retries and a single dial attempt.
const (
	defaultDialTimeout   = 500 * time.Millisecond
	defaultReadTimeout   = 500 * time.Millisecond
	defaultWriteTimeout  = 500 * time.Millisecond
	defaultPoolTimeout   = 1 * time.Second
	defaultDialerRetries = 1
)

func defaultClientTimeouts() ClientTimeouts {
	return ClientTimeouts{
		DialTimeout:     defaultDialTimeout,
		ReadTimeout:     defaultReadTimeout,
		WriteTimeout:    defaultWriteTimeout,
		PoolTimeout:     defaultPoolTimeout,
		MaxRetries:      0,
		DialerRetries:   defaultDialerRetries,
		MinRetryBackoff: 0,
		MaxRetryBackoff: 0,
	}
}

// ResolveClientTimeouts returns the timeouts that New() will apply for cfg.
func ResolveClientTimeouts(cfg Config) ClientTimeouts {
	t := defaultClientTimeouts()
	if cfg.DialTimeout > 0 {
		t.DialTimeout = cfg.DialTimeout
	}
	if cfg.ReadTimeout > 0 {
		t.ReadTimeout = cfg.ReadTimeout
	}
	if cfg.WriteTimeout > 0 {
		t.WriteTimeout = cfg.WriteTimeout
	}
	if cfg.PoolTimeout > 0 {
		t.PoolTimeout = cfg.PoolTimeout
	}
	if cfg.DialerRetries > 0 {
		t.DialerRetries = cfg.DialerRetries
	}
	if cfg.MaxRetries >= 0 {
		t.MaxRetries = cfg.MaxRetries
	}
	return t
}

// goRedisMaxRetries maps application MaxRetries to go-redis semantics (0 => disable).
func goRedisMaxRetries(maxRetries int) int {
	if maxRetries <= 0 {
		return -1
	}
	return maxRetries
}

func loadDurationEnvMS(key string, current time.Duration) time.Duration {
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

func loadPositiveIntEnv(key string, current int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return current
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return current
	}
	return n
}

func loadNonNegativeIntEnv(key string, current int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return current
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return current
	}
	return n
}
