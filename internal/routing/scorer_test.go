package routing

import (
	"testing"
)

func TestComputeScore_HealthyGateway(t *testing.T) {
	cfg := DefaultConfig()
	state := GatewayState{
		Gateway:      Gateway{ID: "gateway-a", Weight: 100},
		Enabled:      true,
		LatencyEMAMs: 20,
		SuccessCount: 95,
		ErrorCount:   5,
		HealthScore:  90,
	}
	score := ComputeScore(state, cfg)
	if score <= 0 {
		t.Fatalf("expected positive score, got %f", score)
	}
}

func TestComputeScore_CircuitOpenExcluded(t *testing.T) {
	cfg := DefaultConfig()
	state := GatewayState{
		Gateway:      Gateway{ID: "gateway-c", Weight: 100},
		Enabled:      true,
		CircuitState: "open",
		HealthScore:  10,
	}
	if ComputeScore(state, cfg) != 0 {
		t.Fatal("circuit open gateway should score 0")
	}
}

func TestComputeScore_HighErrorRateStillScores(t *testing.T) {
	cfg := DefaultConfig()
	state := GatewayState{
		Gateway:      Gateway{ID: "gateway-c", Weight: 100},
		Enabled:      true,
		SuccessCount: 2,
		ErrorCount:   18,
		HealthScore:  30,
	}
	if ComputeScore(state, cfg) <= 0 {
		t.Fatal("high error rate lowers score but circuit breaker handles trip")
	}
}

// Asserts the weighted-roll arithmetic by driving the roll source directly.
// Sampling the real source instead would be flaky: gateway c scores above zero,
// so an unseeded run can legitimately pick it.
func TestSelector_PickPrimaryWeighted(t *testing.T) {
	cfg := DefaultConfig()
	states := []GatewayState{
		{Gateway: Gateway{ID: "a", Weight: 100}, Enabled: true, HealthScore: 95, LatencyEMAMs: 10, SuccessCount: 100},
		{Gateway: Gateway{ID: "b", Weight: 50}, Enabled: true, HealthScore: 80, LatencyEMAMs: 50, SuccessCount: 100},
		{Gateway: Gateway{ID: "c", Weight: 10}, Enabled: true, HealthScore: 20, LatencyEMAMs: 200, SuccessCount: 100},
	}

	var candidates []ScoredGateway
	for _, g := range RankScores(states, cfg) {
		if g.Score > 0 {
			candidates = append(candidates, g)
		}
	}
	if len(candidates) < 2 {
		t.Fatalf("need at least two selectable gateways, got %d", len(candidates))
	}

	sel := NewSelector(cfg)

	sel.roll = func() float64 { return 0 }
	picked, ok := sel.PickPrimary(states)
	if !ok {
		t.Fatal("expected pick")
	}
	if picked.State.ID != candidates[0].State.ID {
		t.Fatalf("lowest roll must select the highest-scoring gateway %s, got %s",
			candidates[0].State.ID, picked.State.ID)
	}

	sel.roll = func() float64 { return 0.999999 }
	picked, ok = sel.PickPrimary(states)
	if !ok {
		t.Fatal("expected pick")
	}
	last := candidates[len(candidates)-1]
	if picked.State.ID != last.State.ID {
		t.Fatalf("highest roll must select the lowest-scoring gateway %s, got %s",
			last.State.ID, picked.State.ID)
	}
}

func TestRankScores_Order(t *testing.T) {
	cfg := DefaultConfig()
	states := []GatewayState{
		{Gateway: Gateway{ID: "slow", Weight: 100}, Enabled: true, HealthScore: 90, LatencyEMAMs: 200, SuccessCount: 50},
		{Gateway: Gateway{ID: "fast", Weight: 100}, Enabled: true, HealthScore: 90, LatencyEMAMs: 10, SuccessCount: 50},
	}
	ranked := RankScores(states, cfg)
	if ranked[0].State.ID != "fast" {
		t.Fatalf("fast gateway should rank first, got %s", ranked[0].State.ID)
	}
}
