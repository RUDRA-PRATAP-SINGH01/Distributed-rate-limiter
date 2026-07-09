package main

import (
	"context"
	"sync"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/logging"
)

var (
	redisHealthMu sync.Mutex
	redisHealthOK *bool
)

func logRedisHealthTransition(ctx context.Context, connected bool, errMsg string) {
	redisHealthMu.Lock()
	defer redisHealthMu.Unlock()

	prev := redisHealthOK
	if prev != nil && *prev == connected {
		return
	}
	redisHealthOK = new(bool)
	*redisHealthOK = connected

	if prev == nil {
		if !connected {
			logging.Warn(ctx, "redis health degraded",
				"component", "limiter",
				"operation", "health_check",
				"error", errMsg,
			)
		}
		return
	}
	if connected {
		logging.Info(ctx, "redis health recovered",
			"component", "limiter",
			"operation", "health_check",
		)
		return
	}
	logging.Warn(ctx, "redis health degraded",
		"component", "limiter",
		"operation", "health_check",
		"error", errMsg,
	)
}
