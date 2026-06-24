// Redis client factory with standalone and Sentinel failover support.
package redis

import (
	"context"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

var Ctx = context.Background()

// NewClient creates a standalone Redis client (legacy helper).
func NewClient(addr, password string) redis.UniversalClient {
	cfg := DefaultConfig()
	cfg.Addr = addr
	cfg.Password = password
	return New(cfg)
}

// New returns a UniversalClient for standalone or Sentinel mode.
// FailoverClient handles master election, replica promotion, and automatic reconnection.
func New(cfg Config) redis.UniversalClient {
	pool := cfg.PoolSize
	if pool <= 0 {
		pool = 100
	}
	minIdle := cfg.MinIdleConns
	if minIdle <= 0 {
		minIdle = 10
	}

	switch cfg.Mode {
	case ModeSentinel:
		if len(cfg.SentinelAddrs) == 0 {
			panic("REDIS_MODE=sentinel requires REDIS_SENTINEL_ADDRS")
		}
		opts := &redis.FailoverOptions{
			MasterName:       cfg.MasterName,
			SentinelAddrs:    cfg.SentinelAddrs,
			Password:         cfg.Password,
			SentinelPassword: cfg.SentinelPassword,
			DB:               cfg.DB,
			PoolSize:         pool,
			MinIdleConns:     minIdle,
			// Sentinel-driven topology updates; go-redis reconnects to promoted master.
			RouteByLatency: false,
			RouteRandomly:  false,
		}
		return redis.NewFailoverClient(opts)
	default:
		opts := &redis.Options{
			Addr:         cfg.Addr,
			Password:     cfg.Password,
			DB:           cfg.DB,
			PoolSize:     pool,
			MinIdleConns: minIdle,
		}
		return redis.NewClient(opts)
	}
}

func Ping(client redis.UniversalClient) error {
	return client.Ping(Ctx).Err()
}

// Describe returns a human-readable connection summary for logs and health.
func Describe(cfg Config) string {
	switch cfg.Mode {
	case ModeSentinel:
		return fmt.Sprintf("sentinel master=%s sentinels=[%s]", cfg.MasterName, strings.Join(cfg.SentinelAddrs, ","))
	default:
		return fmt.Sprintf("standalone addr=%s", cfg.Addr)
	}
}

// Close shuts down the underlying connection pool.
func Close(client redis.UniversalClient) error {
	if client == nil {
		return nil
	}
	return client.Close()
}
