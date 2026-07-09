package redis

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestResolveClientTimeoutsDefaults(t *testing.T) {
	tm := ResolveClientTimeouts(DefaultConfig())
	if tm.DialTimeout != 500*time.Millisecond {
		t.Fatalf("DialTimeout=%v", tm.DialTimeout)
	}
	if tm.ReadTimeout != 500*time.Millisecond || tm.WriteTimeout != 500*time.Millisecond {
		t.Fatalf("read/write timeouts: %+v", tm)
	}
	if tm.PoolTimeout != time.Second {
		t.Fatalf("PoolTimeout=%v", tm.PoolTimeout)
	}
	if tm.MaxRetries != 0 || tm.DialerRetries != 1 {
		t.Fatalf("retries: %+v", tm)
	}
}

func TestLoadConfigTimeoutEnv(t *testing.T) {
	t.Setenv("REDIS_DIAL_TIMEOUT_MS", "750")
	t.Setenv("REDIS_READ_TIMEOUT_MS", "600")
	t.Setenv("REDIS_WRITE_TIMEOUT_MS", "650")
	t.Setenv("REDIS_POOL_TIMEOUT_MS", "1200")
	t.Setenv("REDIS_DIALER_RETRIES", "2")
	t.Setenv("REDIS_MAX_RETRIES", "1")

	cfg := LoadConfigFromEnv()
	tm := ResolveClientTimeouts(cfg)
	if tm.DialTimeout != 750*time.Millisecond {
		t.Fatalf("DialTimeout=%v", tm.DialTimeout)
	}
	if tm.ReadTimeout != 600*time.Millisecond || tm.WriteTimeout != 650*time.Millisecond {
		t.Fatalf("read/write: %+v", tm)
	}
	if tm.PoolTimeout != 1200*time.Millisecond {
		t.Fatalf("PoolTimeout=%v", tm.PoolTimeout)
	}
	if tm.DialerRetries != 2 || tm.MaxRetries != 1 {
		t.Fatalf("retries: %+v", tm)
	}
}

func TestLoadConfigInvalidTimeoutEnvKeepsDefaults(t *testing.T) {
	t.Setenv("REDIS_DIAL_TIMEOUT_MS", "nope")
	t.Setenv("REDIS_MAX_RETRIES", "-5")
	cfg := LoadConfigFromEnv()
	tm := ResolveClientTimeouts(cfg)
	if tm.DialTimeout != defaultDialTimeout {
		t.Fatalf("expected default dial timeout, got %v", tm.DialTimeout)
	}
	if tm.MaxRetries != 0 {
		t.Fatalf("expected default max retries 0, got %d", tm.MaxRetries)
	}
}

func TestGoRedisMaxRetriesMapping(t *testing.T) {
	if goRedisMaxRetries(0) != -1 {
		t.Fatal("0 should disable retries in go-redis")
	}
	if goRedisMaxRetries(2) != 2 {
		t.Fatal("positive retries should pass through")
	}
}

func TestNewStandaloneAppliesBoundedTimeouts(t *testing.T) {
	client := New(DefaultConfig())
	defer client.Close()

	standalone, ok := client.(*redis.Client)
	if !ok {
		t.Fatalf("expected *redis.Client, got %T", client)
	}
	opts := standalone.Options()
	if opts.DialTimeout != 500*time.Millisecond {
		t.Fatalf("DialTimeout=%v", opts.DialTimeout)
	}
	if opts.ReadTimeout != 500*time.Millisecond || opts.WriteTimeout != 500*time.Millisecond {
		t.Fatalf("read/write: %v %v", opts.ReadTimeout, opts.WriteTimeout)
	}
	if opts.PoolTimeout != time.Second {
		t.Fatalf("PoolTimeout=%v", opts.PoolTimeout)
	}
	if opts.MaxRetries != 0 {
		t.Fatalf("MaxRetries=%d want 0", opts.MaxRetries)
	}
	if opts.DialerRetries != 1 {
		t.Fatalf("DialerRetries=%d", opts.DialerRetries)
	}
}

func TestNewSentinelAppliesBoundedTimeouts(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Mode = ModeSentinel
	cfg.SentinelAddrs = []string{"127.0.0.1:26379"}

	client := New(cfg)
	defer client.Close()

	failover, ok := client.(*redis.Client)
	if !ok {
		t.Fatalf("expected *redis.Client failover, got %T", client)
	}
	opts := failover.Options()
	if opts.DialTimeout != 500*time.Millisecond || opts.MaxRetries != 0 || opts.DialerRetries != 1 {
		t.Fatalf("unexpected sentinel timeouts: dial=%v retries=%d dialerRetries=%d",
			opts.DialTimeout, opts.MaxRetries, opts.DialerRetries)
	}
}

func TestPingUnreachableBoundedLatency(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Addr = "127.0.0.1:1" // nothing listening
	client := New(cfg)
	defer client.Close()

	start := time.Now()
	err := client.Ping(context.Background()).Err()
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected ping error")
	}
	// Budget: pool (1s) + one dial (500ms) with no command retries.
	if elapsed > 2*time.Second+200*time.Millisecond {
		t.Fatalf("ping took %v, exceeds bounded outage budget", elapsed)
	}
}