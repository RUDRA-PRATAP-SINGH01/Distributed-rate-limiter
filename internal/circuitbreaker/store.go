package circuitbreaker

import (
	"context"
	_ "embed"
	"fmt"
	"strconv"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/metrics"
	"github.com/redis/go-redis/v9"
)

//go:embed lua/allow.lua
var allowLua string

//go:embed lua/record.lua
var recordLua string

// RedisStore persists distributed circuit state in Redis.
type RedisStore struct {
	rdb         *redis.Client
	cfg         Config
	allowScript *redis.Script
	recScript   *redis.Script
}

func NewRedisStore(rdb *redis.Client, cfg Config) *RedisStore {
	return &RedisStore{
		rdb:         rdb,
		cfg:         cfg,
		allowScript: redis.NewScript(allowLua),
		recScript:   redis.NewScript(recordLua),
	}
}

func cbKey(target string) string {
	return fmt.Sprintf("cb:%s", target)
}

// Allow checks whether a call to target may proceed.
func (s *RedisStore) Allow(ctx context.Context, target string) (AllowResult, error) {
	start := time.Now()
	result, err := s.allowScript.Run(ctx, s.rdb, []string{cbKey(target)},
		time.Now().UnixMilli(),
		s.cfg.OpenCooldownMs,
		s.cfg.HalfOpenMaxProbes,
	).Result()
	metrics.RecordCircuitRedisDuration(time.Since(start).Seconds())

	if err != nil {
		return AllowResult{}, err
	}
	values, ok := result.([]interface{})
	if !ok || len(values) < 3 {
		return AllowResult{}, fmt.Errorf("unexpected allow lua result for %s", target)
	}

	allowed := luaInt(values[0]) == 1
	state := stateFromCode(luaInt(values[1]))
	probes := int(luaInt(values[2]))

	out := AllowResult{
		Allowed:         allowed,
		State:           state,
		ProbesRemaining: probes,
	}
	if !allowed {
		switch state {
		case StateOpen:
			out.RejectionReason = "circuit_open"
		case StateHalfOpen:
			out.RejectionReason = "half_open_probe_quota_exhausted"
		}
		metrics.RecordCircuitRejection(target, string(state))
	}
	metrics.RecordCircuitState(target, state)
	return out, nil
}

// Record updates metrics and may transition state after a call completes.
func (s *RedisStore) Record(ctx context.Context, target string, input RecordInput) (Snapshot, error) {
	start := time.Now()
	result, err := s.recScript.Run(ctx, s.rdb, []string{cbKey(target)},
		int(input.Kind),
		input.Latency.Milliseconds(),
		time.Now().UnixMilli(),
		s.cfg.FailureRateThreshold,
		s.cfg.MinSamples,
		s.cfg.ConsecutiveFailures,
		s.cfg.LatencyThresholdMs,
		s.cfg.TimeoutRateThreshold,
		s.cfg.HalfOpenSuccessRequired,
		s.cfg.EMAAlpha,
	).Result()
	metrics.RecordCircuitRedisDuration(time.Since(start).Seconds())

	if err != nil {
		return Snapshot{}, err
	}
	values, ok := result.([]interface{})
	if !ok || len(values) < 6 || luaInt(values[0]) != 1 {
		return Snapshot{}, fmt.Errorf("record failed for %s", target)
	}

	state := stateFromCode(luaInt(values[1]))
	prev := stateFromCode(luaInt(values[2]))
	transition := luaString(values[3])
	failureRate, _ := strconv.ParseFloat(luaString(values[4]), 64)
	latencyEMA, _ := strconv.ParseFloat(luaString(values[5]), 64)

	metrics.RecordCircuitOutcome(target, outcomeLabel(input.Kind))
	metrics.RecordCircuitState(target, state)
	if transition != "none" && prev != state {
		metrics.RecordCircuitTransition(target, string(prev), string(state))
	}
	metrics.RecordCircuitFailureRate(target, failureRate)
	metrics.RecordCircuitLatencyEMA(target, latencyEMA)

	snap, err := s.GetState(ctx, target)
	if err != nil {
		snap = Snapshot{Target: target, State: state, FailureRate: failureRate, LatencyEMAMs: latencyEMA}
	}
	return snap, nil
}

// GetState reads current circuit snapshot from Redis.
func (s *RedisStore) GetState(ctx context.Context, target string) (Snapshot, error) {
	fields, err := s.rdb.HGetAll(ctx, cbKey(target)).Result()
	if err != nil {
		return Snapshot{}, err
	}
	if len(fields) == 0 {
		return Snapshot{Target: target, State: StateClosed}, nil
	}
	return parseSnapshot(target, fields), nil
}

// Reset forces closed state (ops recovery).
func (s *RedisStore) Reset(ctx context.Context, target string) error {
	key := cbKey(target)
	pipe := s.rdb.Pipeline()
	pipe.Del(ctx, key)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return err
	}
	metrics.RecordCircuitState(target, StateClosed)
	metrics.RecordCircuitTransition(target, "any", string(StateClosed))
	return nil
}

// ListTargets returns all circuit breaker keys.
func (s *RedisStore) ListTargets(ctx context.Context) ([]Snapshot, error) {
	var cursor uint64
	var keys []string
	for {
		batch, next, err := s.rdb.Scan(ctx, cursor, "cb:*", 100).Result()
		if err != nil {
			return nil, err
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	out := make([]Snapshot, 0, len(keys))
	for _, key := range keys {
		target := key[3:] // strip "cb:"
		snap, err := s.GetState(ctx, target)
		if err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	return out, nil
}

func parseSnapshot(target string, fields map[string]string) Snapshot {
	total := luaInt(fields["total_count"])
	fail := luaInt(fields["failure_count"])
	timeouts := luaInt(fields["timeout_count"])
	var failureRate, timeoutRate float64
	if total > 0 {
		failureRate = float64(fail) / float64(total)
		timeoutRate = float64(timeouts) / float64(total)
	}
	return Snapshot{
		Target:              target,
		State:               normalizeState(fields["state"]),
		FailureRate:         failureRate,
		TimeoutRate:         timeoutRate,
		LatencyEMAMs:        parseFloat(fields["latency_ema_ms"]),
		ConsecutiveFailures: luaInt(fields["consecutive_failures"]),
		SuccessCount:        luaInt(fields["success_count"]),
		FailureCount:        fail,
		TimeoutCount:        timeouts,
		LatencySpikeCount:   luaInt(fields["latency_spike_count"]),
		TotalCount:          total,
		HalfOpenCalls:       luaInt(fields["half_open_calls"]),
		HalfOpenSuccesses:   luaInt(fields["half_open_successes"]),
		OpenedAtMs:          luaInt(fields["opened_at"]),
		HalfOpenAtMs:        luaInt(fields["half_open_at"]),
		UpdatedAtMs:         luaInt(fields["updated_at"]),
	}
}

func outcomeLabel(k OutcomeKind) string {
	switch k {
	case OutcomeSuccess:
		return "success"
	case OutcomeTimeout:
		return "timeout"
	case OutcomeLatencySpike:
		return "latency_spike"
	default:
		return "failure"
	}
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

func normalizeState(raw string) State {
	if raw == "" {
		return StateClosed
	}
	return State(raw)
}
