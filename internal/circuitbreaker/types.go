package circuitbreaker

import "time"

// State is the circuit breaker phase.
type State string

const (
	StateClosed   State = "closed"
	StateOpen     State = "open"
	StateHalfOpen State = "half_open"
)

// OutcomeKind classifies one dependency call result.
type OutcomeKind int

const (
	OutcomeSuccess OutcomeKind = iota
	OutcomeFailure
	OutcomeTimeout
	OutcomeLatencySpike
)

// Well-known targets across the fleet.
const (
	TargetRedis          = "redis"
	TargetCentralLimiter = "central-limiter"
)

// RecordInput is passed to Record after a protected call completes.
type RecordInput struct {
	Kind    OutcomeKind
	Latency time.Duration
}

// AllowResult is returned by Allow before a protected call.
type AllowResult struct {
	Allowed           bool
	State             State
	ProbesRemaining   int
	RejectionReason   string
}

// Snapshot is the live circuit state stored in Redis.
type Snapshot struct {
	Target              string  `json:"target"`
	State               State   `json:"state"`
	FailureRate         float64 `json:"failure_rate"`
	TimeoutRate         float64 `json:"timeout_rate"`
	LatencyEMAMs        float64 `json:"latency_ema_ms"`
	ConsecutiveFailures int64   `json:"consecutive_failures"`
	SuccessCount        int64   `json:"success_count"`
	FailureCount        int64   `json:"failure_count"`
	TimeoutCount        int64   `json:"timeout_count"`
	LatencySpikeCount   int64   `json:"latency_spike_count"`
	TotalCount          int64   `json:"total_count"`
	HalfOpenCalls       int64   `json:"half_open_calls"`
	HalfOpenSuccesses   int64   `json:"half_open_successes"`
	OpenedAtMs          int64   `json:"opened_at_ms,omitempty"`
	HalfOpenAtMs        int64   `json:"half_open_at_ms,omitempty"`
	UpdatedAtMs         int64   `json:"updated_at_ms"`
}

func (s State) Code() int {
	switch s {
	case StateOpen:
		return 1
	case StateHalfOpen:
		return 2
	default:
		return 0
	}
}

func stateFromCode(code int64) State {
	switch code {
	case 1:
		return StateOpen
	case 2:
		return StateHalfOpen
	default:
		return StateClosed
	}
}
