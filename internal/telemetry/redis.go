package telemetry

import (
	"fmt"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
)

// InstrumentRedis adds OpenTelemetry tracing to all Redis commands that receive a context.
func InstrumentRedis(rdb *redis.Client) error {
	if err := redisotel.InstrumentTracing(rdb); err != nil {
		return fmt.Errorf("redis tracing: %w", err)
	}
	if err := redisotel.InstrumentMetrics(rdb); err != nil {
		return fmt.Errorf("redis metrics: %w", err)
	}
	return nil
}
