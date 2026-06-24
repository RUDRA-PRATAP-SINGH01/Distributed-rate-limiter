package routing

import "math"

// ComputeScore returns a routing weight for weighted selection.
// Higher score = more traffic. Zero score = excluded.
func ComputeScore(state GatewayState, cfg Config) float64 {
	if !state.Selectable(cfg) {
		return 0
	}

	weight := float64(state.Weight)
	if weight <= 0 {
		weight = 1
	}

	latency := state.LatencyEMAMs
	if latency < 1 {
		latency = 1
	}
	latencyFactor := cfg.TargetLatencyMs / latency
	if latencyFactor > 2.0 {
		latencyFactor = 2.0
	}
	if latencyFactor < 0.1 {
		latencyFactor = 0.1
	}

	healthFactor := state.HealthScore / 100.0
	if healthFactor < 0 {
		healthFactor = 0
	}

	errRate := state.ErrorRate()
	errorFactor := 1.0 - (errRate * cfg.ErrorPenalty)
	if errorFactor < 0.05 {
		errorFactor = 0.05
	}

	score := weight * latencyFactor * healthFactor * errorFactor
	if math.IsNaN(score) || score < 0 {
		return 0
	}
	return score
}

// RankScores sorts gateways by score descending for failover ordering.
func RankScores(states []GatewayState, cfg Config) []ScoredGateway {
	out := make([]ScoredGateway, 0, len(states))
	for _, s := range states {
		out = append(out, ScoredGateway{State: s, Score: ComputeScore(s, cfg)})
	}
	// insertion sort — small N (3-10 gateways)
	for i := 1; i < len(out); i++ {
		j := i
		for j > 0 && out[j].Score > out[j-1].Score {
			out[j], out[j-1] = out[j-1], out[j]
			j--
		}
	}
	return out
}
