package circuitbreaker

import (
	"context"
	"sync"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/logging"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/metrics"
)

// LocalStore keeps circuit state in process memory.
//
// This is the correct backend when protecting Redis: Allow/Record never touch
// Redis, so the breaker can open and fail-fast while Redis is down or slow.
// State is per-process (correct for this dependency — one node's Redis pain
// should not open the circuit on healthy peers).
type LocalStore struct {
	cfg Config
	mu  sync.Mutex
	m   map[string]*localCircuit
}

type localCircuit struct {
	state               State
	consecutiveFailures int64
	successCount        int64
	failureCount        int64
	timeoutCount        int64
	latencySpikeCount   int64
	totalCount          int64
	latencyEMAMs        float64
	halfOpenCalls       int64
	halfOpenSuccesses   int64
	openedAt            time.Time
	halfOpenAt          time.Time
	updatedAt           time.Time
}

func NewLocalStore(cfg Config) *LocalStore {
	return &LocalStore{
		cfg: cfg,
		m:   make(map[string]*localCircuit),
	}
}

func (s *LocalStore) getOrCreateLocked(target string) *localCircuit {
	c, ok := s.m[target]
	if ok {
		return c
	}
	c = &localCircuit{state: StateClosed}
	s.m[target] = c
	return c
}

func (s *LocalStore) Allow(_ context.Context, target string) (AllowResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c := s.getOrCreateLocked(target)
	now := time.Now()
	c.updatedAt = now

	switch c.state {
	case StateOpen:
		if now.Sub(c.openedAt) >= time.Duration(s.cfg.OpenCooldownMs)*time.Millisecond {
			c.state = StateHalfOpen
			c.halfOpenAt = now
			c.halfOpenCalls = 0
			c.halfOpenSuccesses = 0
		} else {
			metrics.RecordCircuitRejection(target, string(StateOpen))
			metrics.RecordCircuitState(target, StateOpen)
			return AllowResult{
				Allowed:         false,
				State:           StateOpen,
				RejectionReason: "circuit_open",
			}, nil
		}
		fallthrough
	case StateHalfOpen:
		if c.halfOpenCalls >= s.cfg.HalfOpenMaxProbes {
			// Missing halfOpenAt (legacy in-memory state) is treated as "just started".
			if c.halfOpenAt.IsZero() {
				c.halfOpenAt = now
			}
			// Check if half-open trial timed out without recovery (e.g. hung in-flight probes)
			if now.Sub(c.halfOpenAt) >= time.Duration(s.cfg.OpenCooldownMs)*time.Millisecond {
				c.state = StateOpen
				c.openedAt = now
				c.halfOpenCalls = 0
				c.halfOpenSuccesses = 0
				metrics.RecordCircuitRejection(target, string(StateOpen))
				metrics.RecordCircuitState(target, StateOpen)
				return AllowResult{
					Allowed:         false,
					State:           StateOpen,
					RejectionReason: "half_open_timeout_reopened",
				}, nil
			}
			// Probe budget currently in flight: stay half-open, reject excess calls without reopening (H-08)
			metrics.RecordCircuitRejection(target, string(StateHalfOpen))
			metrics.RecordCircuitState(target, StateHalfOpen)
			return AllowResult{
				Allowed:         false,
				State:           StateHalfOpen,
				ProbesRemaining: 0,
				RejectionReason: "half_open_probe_in_flight",
			}, nil
		}
		c.halfOpenCalls++
		remaining := int(s.cfg.HalfOpenMaxProbes - c.halfOpenCalls)
		metrics.RecordCircuitState(target, StateHalfOpen)
		return AllowResult{
			Allowed:         true,
			State:           StateHalfOpen,
			ProbesRemaining: remaining,
		}, nil
	default:
		metrics.RecordCircuitState(target, StateClosed)
		return AllowResult{Allowed: true, State: StateClosed, ProbesRemaining: -1}, nil
	}
}

func (s *LocalStore) Record(ctx context.Context, target string, input RecordInput) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c := s.getOrCreateLocked(target)
	prev := c.state
	now := time.Now()
	c.updatedAt = now

	latencyMs := float64(input.Latency.Milliseconds())
	if c.latencyEMAMs <= 0 {
		c.latencyEMAMs = latencyMs
	} else {
		alpha := s.cfg.EMAAlpha
		c.latencyEMAMs = alpha*latencyMs + (1-alpha)*c.latencyEMAMs
	}

	c.totalCount++
	switch input.Kind {
	case OutcomeSuccess:
		c.successCount++
		c.consecutiveFailures = 0
	case OutcomeTimeout:
		c.timeoutCount++
		c.failureCount++
		c.consecutiveFailures++
	case OutcomeLatencySpike:
		c.latencySpikeCount++
		c.failureCount++
		c.consecutiveFailures++
	default:
		c.failureCount++
		c.consecutiveFailures++
	}

	// Decay counters like record.lua when the window grows large.
	if c.totalCount > 1000 {
		c.successCount /= 2
		c.failureCount /= 2
		c.timeoutCount /= 2
		c.latencySpikeCount /= 2
		c.totalCount /= 2
	}

	var failureRate, timeoutRate float64
	if c.totalCount > 0 {
		failureRate = float64(c.failureCount) / float64(c.totalCount)
		timeoutRate = float64(c.timeoutCount) / float64(c.totalCount)
	}

	transition := "none"
	switch c.state {
	case StateHalfOpen:
		if input.Kind == OutcomeSuccess {
			c.halfOpenSuccesses++
			if c.halfOpenSuccesses >= s.cfg.HalfOpenSuccessRequired {
				c.state = StateClosed
				c.consecutiveFailures = 0
				c.halfOpenCalls = 0
				c.halfOpenSuccesses = 0
				transition = "closed"
			}
		} else {
			c.state = StateOpen
			c.openedAt = now
			transition = "reopened"
		}
	case StateClosed:
		trip := false
		if c.totalCount >= s.cfg.MinSamples && failureRate >= s.cfg.FailureRateThreshold {
			trip = true
		}
		if c.consecutiveFailures >= s.cfg.ConsecutiveFailures {
			trip = true
		}
		if s.cfg.LatencyThresholdMs > 0 &&
			input.Latency.Milliseconds() >= s.cfg.LatencyThresholdMs &&
			c.latencyEMAMs >= float64(s.cfg.LatencyThresholdMs) {
			trip = true
		}
		if c.totalCount >= s.cfg.MinSamples && timeoutRate >= s.cfg.TimeoutRateThreshold {
			trip = true
		}
		if trip {
			c.state = StateOpen
			c.openedAt = now
			transition = "opened"
		}
	}

	metrics.RecordCircuitOutcome(target, outcomeLabel(input.Kind))
	metrics.RecordCircuitState(target, c.state)
	if transition != "none" && prev != c.state {
		metrics.RecordCircuitTransition(target, string(prev), string(c.state))
		logging.Warn(ctx, "circuit breaker state transition",
			"component", "circuit_breaker",
			"backend", "local",
			"target", target,
			"from_state", string(prev),
			"to_state", string(c.state),
		)
	}
	metrics.RecordCircuitFailureRate(target, failureRate)
	metrics.RecordCircuitLatencyEMA(target, c.latencyEMAMs)

	return s.snapshotLocked(target, c), nil
}

func (s *LocalStore) GetState(_ context.Context, target string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.m[target]
	if !ok {
		return Snapshot{Target: target, State: StateClosed}, nil
	}
	return s.snapshotLocked(target, c), nil
}

func (s *LocalStore) Reset(_ context.Context, target string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, target)
	metrics.RecordCircuitState(target, StateClosed)
	metrics.RecordCircuitTransition(target, "any", string(StateClosed))
	return nil
}

func (s *LocalStore) ListTargets(_ context.Context) ([]Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Snapshot, 0, len(s.m))
	for target, c := range s.m {
		out = append(out, s.snapshotLocked(target, c))
	}
	return out, nil
}

func (s *LocalStore) snapshotLocked(target string, c *localCircuit) Snapshot {
	var failureRate, timeoutRate float64
	if c.totalCount > 0 {
		failureRate = float64(c.failureCount) / float64(c.totalCount)
		timeoutRate = float64(c.timeoutCount) / float64(c.totalCount)
	}
	snap := Snapshot{
		Target:              target,
		State:               c.state,
		FailureRate:         failureRate,
		TimeoutRate:         timeoutRate,
		LatencyEMAMs:        c.latencyEMAMs,
		ConsecutiveFailures: c.consecutiveFailures,
		SuccessCount:        c.successCount,
		FailureCount:        c.failureCount,
		TimeoutCount:        c.timeoutCount,
		LatencySpikeCount:   c.latencySpikeCount,
		TotalCount:          c.totalCount,
		HalfOpenCalls:       c.halfOpenCalls,
		HalfOpenSuccesses:   c.halfOpenSuccesses,
		UpdatedAtMs:         c.updatedAt.UnixMilli(),
	}
	if !c.openedAt.IsZero() {
		snap.OpenedAtMs = c.openedAt.UnixMilli()
	}
	if !c.halfOpenAt.IsZero() {
		snap.HalfOpenAtMs = c.halfOpenAt.UnixMilli()
	}
	return snap
}
