package limiter

import (
    "context"
    "fmt"
    "strconv"
    "time"
    "github.com/redis/go-redis/v9"
)

type RedisTokenBucket struct {
    rdb        *redis.Client
    capacity   int
    refillRate float64
}

func NewRedisTokenBucket(rdb *redis.Client, capacity int, refillRate float64) *RedisTokenBucket {
    return &RedisTokenBucket{
        rdb:        rdb,
        capacity:   capacity,
        refillRate: refillRate,
    }
}

func (tb *RedisTokenBucket) Allow(userID string) (bool, int) {
    ctx := context.Background()
    key := fmt.Sprintf("rate:%s", userID)

    // Read current state
    tokensStr, err := tb.rdb.HGet(ctx, key, "tokens").Result()
    var tokens float64
    var lastRefill time.Time

    if err == redis.Nil {
        tokens = float64(tb.capacity)
        lastRefill = time.Now()
    } else if err != nil {
        return false, 0
    } else {
        tokens, _ = strconv.ParseFloat(tokensStr, 64)
        lastRefillStr, _ := tb.rdb.HGet(ctx, key, "last_refill").Result()
        lastRefillUnix, _ := strconv.ParseInt(lastRefillStr, 10, 64)
        lastRefill = time.Unix(lastRefillUnix, 0)
    }

    // Refill logic
    now := time.Now()
    elapsed := now.Sub(lastRefill).Seconds()
    newTokens := tokens + elapsed*tb.refillRate
    if newTokens > float64(tb.capacity) {
        newTokens = float64(tb.capacity)
    }

    allowed := false
    if newTokens >= 1.0 {
        newTokens -= 1.0
        allowed = true
    }

    // Write back
    tb.rdb.HSet(ctx, key, "tokens", newTokens, "last_refill", now.Unix())
    tb.rdb.Expire(ctx, key, 1*time.Hour)

    return allowed, int(newTokens)
}