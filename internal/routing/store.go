package routing

import (
	"context"
	_ "embed"
	"fmt"
	"strconv"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/metrics"
	"github.com/redis/go-redis/v9"
)

//go:embed lua/record_outcome.lua
var recordOutcomeLua string

// RedisStore persists gateway definitions and live metrics in Redis.
type RedisStore struct {
	rdb    *redis.Client
	cfg    Config
	script *redis.Script
}

func NewRedisStore(rdb *redis.Client, cfg Config) *RedisStore {
	return &RedisStore{
		rdb:    rdb,
		cfg:    cfg,
		script: redis.NewScript(recordOutcomeLua),
	}
}

func gwKey(id string) string {
	return fmt.Sprintf("route:gw:%s", id)
}

func indexKey() string {
	return "route:index"
}

// RegisterGateway upserts a gateway definition.
func (s *RedisStore) RegisterGateway(ctx context.Context, gw Gateway) error {
	key := gwKey(gw.ID)
	exists, err := s.rdb.Exists(ctx, key).Result()
	if err != nil {
		return err
	}
	if exists == 0 {
		err = s.rdb.HSet(ctx, key, map[string]interface{}{
			"id": gw.ID, "url": gw.URL, "weight": gw.Weight,
			"enabled": 1, "health_score": 100, "latency_ema_ms": 0,
			"success_count": 0, "error_count": 0, "total_requests": 0,
			"circuit_open": 0, "circuit_opened_at": 0,
			"updated_at": time.Now().UnixMilli(),
		}).Err()
	} else {
		err = s.rdb.HSet(ctx, key, "url", gw.URL, "weight", gw.Weight).Err()
	}
	if err != nil {
		return err
	}
	return s.rdb.SAdd(ctx, indexKey(), gw.ID).Err()
}

// ListGateways returns all registered gateway states.
func (s *RedisStore) ListGateways(ctx context.Context) ([]GatewayState, error) {
	ids, err := s.rdb.SMembers(ctx, indexKey()).Result()
	if err != nil {
		return nil, err
	}
	out := make([]GatewayState, 0, len(ids))
	for _, id := range ids {
		st, err := s.GetGateway(ctx, id)
		if err != nil {
			return nil, err
		}
		if st != nil {
			out = append(out, *st)
		}
	}
	return out, nil
}

// GetGateway reads one gateway state.
func (s *RedisStore) GetGateway(ctx context.Context, id string) (*GatewayState, error) {
	fields, err := s.rdb.HGetAll(ctx, gwKey(id)).Result()
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, nil
	}
	return parseGatewayState(fields), nil
}

// RecordOutcome atomically updates metrics via Lua.
func (s *RedisStore) RecordOutcome(ctx context.Context, outcome Outcome) error {
	start := time.Now()
	result, err := s.script.Run(ctx, s.rdb, []string{gwKey(outcome.GatewayID)},
		boolToInt(outcome.Success),
		outcome.Latency.Milliseconds(),
		s.cfg.EMAAlpha,
		s.cfg.CircuitErrorRate,
		s.cfg.CircuitMinSamples,
		s.cfg.CircuitCooldownMs,
		time.Now().UnixMilli(),
	).Result()
	metrics.RecordRoutingRedisDuration(time.Since(start).Seconds())

	if err != nil {
		metrics.RecordRoutingOutcome(outcome.GatewayID, "error")
		return err
	}
	values, ok := result.([]interface{})
	if !ok || len(values) < 1 || luaInt(values[0]) != 1 {
		metrics.RecordRoutingOutcome(outcome.GatewayID, "error")
		return fmt.Errorf("record outcome failed for %s", outcome.GatewayID)
	}

	resultLabel := "success"
	if !outcome.Success {
		resultLabel = "failure"
	}
	metrics.RecordRoutingOutcome(outcome.GatewayID, resultLabel)
	metrics.RecordRoutingLatency(outcome.GatewayID, outcome.Latency.Seconds())

	if len(values) >= 5 {
		health, _ := strconv.ParseFloat(luaString(values[1]), 64)
		circuit := luaInt(values[4]) == 1
		metrics.RecordRoutingScore(outcome.GatewayID, health)
		if circuit {
			metrics.RecordRoutingCircuitState(outcome.GatewayID, true)
		}
	}
	return nil
}

// SetEnabled toggles gateway participation.
func (s *RedisStore) SetEnabled(ctx context.Context, id string, enabled bool) error {
	val := 0
	if enabled {
		val = 1
	}
	return s.rdb.HSet(ctx, gwKey(id), "enabled", val).Err()
}

// SetWeight updates static routing weight.
func (s *RedisStore) SetWeight(ctx context.Context, id string, weight int) error {
	return s.rdb.HSet(ctx, gwKey(id), "weight", weight).Err()
}

// ResetCircuit manually closes circuit breaker (ops recovery).
func (s *RedisStore) ResetCircuit(ctx context.Context, id string) error {
	return s.rdb.HSet(ctx, gwKey(id), "circuit_open", 0, "error_count", 0).Err()
}

// UpdateHealthProbe sets health from passive /health probe.
func (s *RedisStore) UpdateHealthProbe(ctx context.Context, id string, healthy bool, latency time.Duration) error {
	outcome := Outcome{GatewayID: id, Latency: latency, Success: healthy}
	return s.RecordOutcome(ctx, outcome)
}

func parseGatewayState(fields map[string]string) *GatewayState {
	st := &GatewayState{
		Gateway: Gateway{
			ID:     fields["id"],
			URL:    fields["url"],
			Weight: int(luaInt(fields["weight"])),
		},
		Enabled:        fields["enabled"] != "0",
		LatencyEMAMs:   parseFloat(fields["latency_ema_ms"]),
		ErrorCount:     luaInt(fields["error_count"]),
		SuccessCount:   luaInt(fields["success_count"]),
		TotalRequests:  luaInt(fields["total_requests"]),
		HealthScore:    parseFloat(fields["health_score"]),
		CircuitOpen:    fields["circuit_open"] == "1",
		CircuitOpenedAt: luaInt(fields["circuit_opened_at"]),
		UpdatedAt:      luaInt(fields["updated_at"]),
	}
	if st.Weight == 0 {
		st.Weight = 100
	}
	return st
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func luaInt(v interface{}) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case string:
		var parsed int64
		fmt.Sscan(n, &parsed)
		return parsed
	default:
		return 0
	}
}

func luaString(v interface{}) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return fmt.Sprint(v)
	}
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
