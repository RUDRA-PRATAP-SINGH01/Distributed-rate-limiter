package routing

import (
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/circuitbreaker"
)

// Gateway is a payment processor / upstream endpoint definition.
type Gateway struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Weight int    `json:"weight"`
}

// GatewayState is live routing state stored in Redis.
type GatewayState struct {
	Gateway
	Enabled         bool                 `json:"enabled"`
	LatencyEMAMs    float64              `json:"latency_ema_ms"`
	ErrorCount      int64                `json:"error_count"`
	SuccessCount    int64                `json:"success_count"`
	TotalRequests   int64                `json:"total_requests"`
	HealthScore     float64              `json:"health_score"`
	CircuitState    circuitbreaker.State `json:"circuit_state"`
	CircuitOpenedAt int64                `json:"circuit_opened_at_ms,omitempty"`
	UpdatedAt       int64                `json:"updated_at_ms"`
}

// CircuitOpen reports whether the breaker is fully open (blocks traffic).
func (s GatewayState) CircuitOpen() bool {
	return s.CircuitState == circuitbreaker.StateOpen
}

// ScoredGateway pairs a gateway with its computed routing score.
type ScoredGateway struct {
	State GatewayState
	Score float64
}

// Outcome records one upstream attempt result.
type Outcome struct {
	GatewayID string
	Latency   time.Duration
	Success   bool
	Timeout   bool
}

// Selectable returns true when the gateway can receive traffic.
func (s GatewayState) Selectable(cfg Config) bool {
	if !s.Enabled {
		return false
	}
	if s.CircuitState == circuitbreaker.StateOpen || s.CircuitState == circuitbreaker.StateUnknown {
		return false
	}
	if s.HealthScore < cfg.MinHealthScore {
		return false
	}
	return true
}

func (s GatewayState) ErrorRate() float64 {
	total := s.SuccessCount + s.ErrorCount
	if total == 0 {
		return 0
	}
	return float64(s.ErrorCount) / float64(total)
}
