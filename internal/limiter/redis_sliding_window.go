package limiter

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/logging"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/luautil"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/metrics"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

//go:embed lua/sliding_window.lua
var slidingWindowLua string

// RedisSlidingWindow implements a sliding-window log on Redis sorted sets: one
// ZSET member per admitted request, scored by arrival time, trimmed on every
// call. Use it when the contract is "at most N requests in any trailing W
// seconds" rather than token-bucket refill.
//
// There is no window reset instant — quota frees up continuously as individual
// entries age out. Memory is bounded at `limit` members per key because a
// member is only added when the window has room.
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

// Allow satisfies the shared RateLimiter contract. Callers that want to send an
// accurate Retry-After should use AllowWithRetryAfter instead.
func (rw *RedisSlidingWindow) Allow(ctx context.Context, userID string) (bool, int, error) {
	allowed, remaining, _, err := rw.AllowWithRetryAfter(ctx, userID)
	return allowed, remaining, err
}

// AllowWithRetryAfter additionally reports how long the caller must wait before
// a slot frees. The duration is zero when the request was allowed, and also
// when the script cannot determine one — callers must treat zero as "no hint"
// and fall back to their own estimate rather than retrying immediately.
func (rw *RedisSlidingWindow) AllowWithRetryAfter(ctx context.Context, userID string) (bool, int, time.Duration, error) {
	key := fmt.Sprintf("sw:%s", userID)
	now := time.Now().UnixMilli()
	windowStart := now - rw.window.Milliseconds()
	member := fmt.Sprintf("%d:%s", now, uuid.NewString()) // unique member avoids ZADD collisions

	// EXPIRE must be at least 1s — Redis rejects sub-second TTL on older configs.
	expireSec := int((rw.window.Milliseconds() + 999) / 1000)
	if expireSec < 1 {
		expireSec = 1
	}

	start := time.Now()
	// ARGV[1]/[2] stay a same-clock (now, windowStart) pair so Lua can take
	// their difference as the window duration. Lua's `now` comes from TIME.
	result, err := rw.script.Run(ctx, rw.rdb, []string{key},
		now,
		windowStart,
		rw.limit,
		expireSec,
		member,
	).Result()
	metrics.RecordRedisDuration(time.Since(start).Seconds())

	if err != nil {
		logging.Error(ctx, "sliding window lua script failed",
			"component", "limiter",
			"operation", "redis_lua",
			"algorithm", "sliding_window",
			"error", err,
		)
		return false, 0, 0, err
	}

	values, ok := result.([]interface{})
	if !ok || len(values) < 2 {
		logging.Error(ctx, "sliding window lua returned unexpected result",
			"component", "limiter",
			"operation", "redis_lua",
			"algorithm", "sliding_window",
		)
		return false, 0, 0, fmt.Errorf("unexpected lua result")
	}

	allowed := luautil.LuaInt(values[0]) == 1
	remaining := int(luautil.LuaInt(values[1]))

	// Tolerate a two-element reply so a rolling deploy against a Redis still
	// serving the previous script degrades to "no hint" instead of failing.
	var retryAfter time.Duration
	if len(values) >= 3 {
		if ms := luautil.LuaInt(values[2]); ms > 0 {
			retryAfter = time.Duration(ms) * time.Millisecond
		}
	}

	return allowed, remaining, retryAfter, nil
}
