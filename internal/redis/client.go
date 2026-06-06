// Redis client factory with connection pooling tuned for high-concurrency limiter traffic.
package redis

import (
	"context"

	"github.com/redis/go-redis/v9"
)

var Ctx = context.Background()

func NewClient(addr, password string) *redis.Client {
	opts := &redis.Options{
		Addr:         addr,
		PoolSize:     100, // sized for k6 load tests; each /check holds a conn for one Lua eval
		MinIdleConns: 10,  // warm pool avoids TCP + AUTH latency on burst traffic
	}
	if password != "" {
		opts.Password = password
	}
	return redis.NewClient(opts)
}

func Ping(client *redis.Client) error {
	return client.Ping(Ctx).Err()
}
