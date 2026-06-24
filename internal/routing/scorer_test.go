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
		Gateway:     Gateway{ID: "gateway-c", Weight: 100},
		Enabled:     true,
		CircuitOpen: true,
		HealthScore: 10,
	}
	if ComputeScore(state, cfg) != 0 {
		t.Fatal("circuit open gateway should score 0")
	}
}

func TestComputeScore_HighErrorRateExcluded(t *testing.T) {
	cfg := DefaultConfig()
	state := GatewayState{
		Gateway:      Gateway{ID: "gateway-c", Weight: 100},
		Enabled:      true,
		SuccessCount: 2,
		ErrorCount:   18,
		HealthScore:  30,
	}
	if ComputeScore(state, cfg) != 0 {
		t.Fatal("high error rate should score 0")
	}
}

func TestSelector_PickPrimaryWeighted(t *testing.T) {
	cfg := DefaultConfig()
	sel := NewSelector(cfg)
	states := []GatewayState{
		{Gateway: Gateway{ID: "a", Weight: 100}, Enabled: true, HealthScore: 95, LatencyEMAMs: 10, SuccessCount: 100},
		{Gateway: Gateway{ID: "b", Weight: 50}, Enabled: true, HealthScore: 80, LatencyEMAMs: 50, SuccessCount: 100},
		{Gateway: Gateway{ID: "c", Weight: 10}, Enabled: true, HealthScore: 20, LatencyEMAMs: 200, SuccessCount: 100},
	}
	picked, ok := sel.PickPrimary(states)
	if !ok {
		t.Fatal("expected pick")
	}
	if picked.State.ID == "c" {
		t.Fatal("unhealthy gateway c should rarely be primary")
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
