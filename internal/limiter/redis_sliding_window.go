package limiter

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/metrics"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/luautil"
	"github.com/redis/go-redis/v9"
)

//go:embed lua/sliding_window.lua
var slidingWindowLua string

// RedisSlidingWindow implements a fixed-window counter in Redis sorted sets.
// Better when you need "max N requests per 60s" rather than smooth token refill.
type RedisSlidingWindow struct {
	rdb    redis.UniversalClient
	limit  int
	window time.Duration
	script *redis.Script
}

func NewRedisSlidingWindow(rdb redis.UniversalClient, limit int, window time.Duration) *RedisSlidingWindow {
	return &RedisSlidingWindow{
		rdb:    rdb,
		limit:  limit,
		window: window,
		script: redis.NewScript(slidingWindowLua),
	}
}

func (rw *RedisSlidingWindow) Allow(ctx context.Context, userID string) (bool, int, error) {
	key := fmt.Sprintf("sw:%s", userID)
	now := time.Now().UnixMilli()
	windowStart := now - rw.window.Milliseconds()
	member := fmt.Sprintf("%d:%d", now, time.Now().UnixNano()) // unique member avoids ZADD collisions

	// EXPIRE must be at least 1s — Redis rejects sub-second TTL on older configs.
	expireSec := int((rw.window.Milliseconds() + 999) / 1000)
	if expireSec < 1 {
		expireSec = 1
	}

	start := time.Now()
	result, err := rw.script.Run(ctx, rw.rdb, []string{key},
		now,
		windowStart,
		rw.limit,
		expireSec,
		member,
	).Result()
	metrics.RecordRedisDuration(time.Since(start).Seconds())

	if err != nil {
		log.Printf("sliding window lua error for %s: %v", userID, err)
		return false, 0, err
	}

	values, ok := result.([]interface{})
	if !ok || len(values) < 2 {
		log.Printf("sliding window lua unexpected result for %s: %#v", userID, result)
		return false, 0, fmt.Errorf("unexpected lua result")
	}

	allowed := luautil.LuaInt(values[0]) == 1
	remaining := int(luautil.LuaInt(values[1]))

	return allowed, remaining, nil
}
