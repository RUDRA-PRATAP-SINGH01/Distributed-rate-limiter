package limiter

import (
    "context"
    _ "embed"
    "fmt"
    "time"

    "github.com/redis/go-redis/v9"
)

//go:embed lua/token_bucket.lua
var luaScript string

type RedisAtomicTokenBucket struct {
    rdb        *redis.Client
    capacity   int
    refillRate float64
    script     *redis.Script
}

func NewRedisAtomicTokenBucket(rdb *redis.Client, capacity int, refillRate float64) *RedisAtomicTokenBucket {
    return &RedisAtomicTokenBucket{
        rdb:        rdb,
        capacity:   capacity,
        refillRate: refillRate,
        script:     redis.NewScript(luaScript),
    }
}

func (tb *RedisAtomicTokenBucket) Allow(userID string) (bool, int) {
    ctx := context.Background()
    key := fmt.Sprintf("rate:%s", userID)
    now := time.Now().Unix()

    result, err := tb.script.Run(ctx, tb.rdb, []string{key},
        tb.capacity,
        tb.refillRate,
        now,
        1, // requested tokens
    ).Result()

    if err != nil {
        return false, 0
    }

    values := result.([]interface{})
    allowed := values[0].(int64) == 1
    remaining := int(values[1].(int64))

    return allowed, remaining
}