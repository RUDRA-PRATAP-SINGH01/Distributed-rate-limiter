package routing

import (
	"math/rand"
	"time"
)

// Selector picks gateways using weighted scores with failover ordering.
type Selector struct {
	cfg Config
	rng *rand.Rand
}

func NewSelector(cfg Config) *Selector {
	return &Selector{
		cfg: cfg,
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
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

	roll := s.rng.Float64() * total
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
