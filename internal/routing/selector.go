package routing

import (
	"math/rand/v2"
)

// Selector picks gateways using weighted scores with failover ordering.
//
// Safe for concurrent use. A Router keeps one Selector and calls PickPrimary
// from every in-flight request, so the weighted roll must not touch shared
// mutable state — a *rand.Rand would be a data race. The top-level math/rand/v2
// source is goroutine-safe without serializing routing behind a mutex.
type Selector struct {
	cfg Config
	// roll returns a uniform float64 in [0,1). Tests replace it to make
	// weighted selection deterministic.
	roll func() float64
}

func NewSelector(cfg Config) *Selector {
	return &Selector{
		cfg:  cfg,
		roll: rand.Float64,
	}
}

// PickPrimary selects one gateway via weighted random among selectable gateways.
func (s *Selector) PickPrimary(states []GatewayState) (ScoredGateway, bool) {
	ranked := RankScores(states, s.cfg)
	var candidates []ScoredGateway
	var total float64
	for _, g := range ranked {
		if g.Score > 0 {
			candidates = append(candidates, g)
			total += g.Score
		}
	}
	if len(candidates) == 0 || total <= 0 {
		return ScoredGateway{}, false
	}

	roll := s.roll() * total
	var acc float64
	for _, c := range candidates {
		acc += c.Score
		if roll <= acc {
			return c, true
		}
	}
	return candidates[len(candidates)-1], true
}

// FailoverOrder returns gateways to try after primary failure (score descending).
func (s *Selector) FailoverOrder(states []GatewayState, excludeID string) []ScoredGateway {
	ranked := RankScores(states, s.cfg)
	out := make([]ScoredGateway, 0, len(ranked))
	for _, g := range ranked {
		if g.State.ID == excludeID || g.Score <= 0 {
			continue
		}
		out = append(out, g)
		if len(out) >= s.cfg.MaxFailoverTries {
			break
		}
	}
	return out
}
