package limiter

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/logging"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/luautil"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/metrics"
	"github.com/redis/go-redis/v9"
)

//go:embed lua/token_bucket.lua
var luaScript string

// RedisAtomicTokenBucket is the production limiter path.
//
// Why Lua: a naive GET-then-SET from Go has a race — two sidecars can both read
// "1 token left" and both allow. EVAL runs refill + deduct atomically on the Redis primary.
type RedisAtomicTokenBucket struct {
	rdb        redis.UniversalClient
	capacity   int
	refillRate float64
	script     *redis.Script
}

func NewRedisAtomicTokenBucket(rdb redis.UniversalClient, capacity int, refillRate float64) *RedisAtomicTokenBucket {
	return &RedisAtomicTokenBucket{
		rdb:        rdb,
		capacity:   capacity,
		refillRate: refillRate,
		script:     redis.NewScript(luaScript),
	}
}

func (tb *RedisAtomicTokenBucket) Allow(ctx context.Context, userID string) (bool, int, error) {
	key := fmt.Sprintf("rate:%s", userID)
	now := time.Now().UnixMilli()

	start := time.Now()
	result, err := tb.script.Run(ctx, tb.rdb, []string{key},
		tb.capacity,
		tb.refillRate,
		now,
		1, // one request consumes one token
	).Result()
	metrics.RecordRedisDuration(time.Since(start).Seconds())

	if err != nil {
		logging.Error(ctx, "token bucket lua script failed",
			"component", "limiter",
			"operation", "redis_lua",
			"algorithm", "token_bucket",
			"error", err,
		)
		return false, 0, err
	}

	values, ok := result.([]interface{})
	if !ok || len(values) < 2 {
		logging.Error(ctx, "token bucket lua returned unexpected result",
			"component", "limiter",
			"operation", "redis_lua",
			"algorithm", "token_bucket",
		)
		return false, 0, fmt.Errorf("unexpected lua result")
	}

	allowed := luautil.LuaInt(values[0]) == 1
	remaining := int(luautil.LuaInt(values[1]))

	return allowed, remaining, nil
}
