package limiter

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

//go:embed lua/hierarchical.lua
var hierarchicalLua string

type HierarchicalLimiter struct {
	rdb          *redis.Client
	script       *redis.Script
	globalCap    int
	globalRate   float64
	tenantCap    int
	tenantRate   float64
	userCap      int
	userRate     float64
	endpointCap  int
	endpointRate float64
}

func NewHierarchicalLimiter(
	rdb *redis.Client,
	globalCap, tenantCap, userCap, endpointCap int,
	globalRate, tenantRate, userRate, endpointRate float64,
) *HierarchicalLimiter {
	return &HierarchicalLimiter{
		rdb:          rdb,
		script:       redis.NewScript(hierarchicalLua),
		globalCap:    globalCap,
		globalRate:   globalRate,
		tenantCap:    tenantCap,
		tenantRate:   tenantRate,
		userCap:      userCap,
		userRate:     userRate,
		endpointCap:  endpointCap,
		endpointRate: endpointRate,
	}
}

func (hl *HierarchicalLimiter) Allow(
	globalKey, tenantKey, userKey, endpointKey string,
) (allowed bool, remaining int, err error) {
	ctx := context.Background()
	now := time.Now().Unix()

	keys := []string{globalKey, tenantKey, userKey, endpointKey}
	args := []interface{}{
		hl.globalCap, hl.tenantCap, hl.userCap, hl.endpointCap,
		hl.globalRate, hl.tenantRate, hl.userRate, hl.endpointRate,
		now,
		1, // requested tokens
	}

	result, err := hl.script.Run(ctx, hl.rdb, keys, args...).Result()
	if err != nil {
		return false, 0, fmt.Errorf("lua error: %w", err)
	}

	values, ok := result.([]interface{})
	if !ok || len(values) < 2 {
		return false, 0, fmt.Errorf("unexpected lua result: %#v", result)
	}

	allowed = luaInt(values[0]) == 1
	remaining = int(luaInt(values[1]))
	return allowed, remaining, nil
}
