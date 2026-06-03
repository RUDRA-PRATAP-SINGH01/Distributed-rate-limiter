package redis

import (
    "context"
    "github.com/redis/go-redis/v9"
)

var Ctx = context.Background()

func NewClient(addr string) *redis.Client {
    return redis.NewClient(&redis.Options{
        Addr: addr,
    })
}