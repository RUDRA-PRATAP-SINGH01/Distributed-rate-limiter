package redis

import (
	"context"

	"github.com/redis/go-redis/v9"
)

var Ctx = context.Background()

func NewClient(addr, password string) *redis.Client {
	opts := &redis.Options{
		Addr:         addr,
		PoolSize:     100,
		MinIdleConns: 10,
	}
	if password != "" {
		opts.Password = password
	}
	return redis.NewClient(opts)
}

func Ping(client *redis.Client) error {
	return client.Ping(Ctx).Err()
}
