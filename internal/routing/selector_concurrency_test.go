package routing

// A Router shares one Selector across every in-flight request, so PickPrimary
// runs concurrently in production. This is the regression guard for that: run
// under -race, it fails if the roll source ever goes back to shared mutable
// state such as *math/rand.Rand.

import (
	"sync"
	"testing"
)

func TestSelector_PickPrimaryIsConcurrencySafe(t *testing.T) {
	cfg := DefaultConfig()
	sel := NewSelector(cfg)
	states := []GatewayState{
		{Gateway: Gateway{ID: "a", Weight: 100}, Enabled: true, HealthScore: 95, LatencyEMAMs: 10, SuccessCount: 100},
		{Gateway: Gateway{ID: "b", Weight: 50}, Enabled: true, HealthScore: 80, LatencyEMAMs: 50, SuccessCount: 100},
		{Gateway: Gateway{ID: "c", Weight: 10}, Enabled: true, HealthScore: 60, LatencyEMAMs: 90, SuccessCount: 100},
	}

	const (
		goroutines = 64
		picksEach  = 200
	)

	var wg sync.WaitGroup
	start := make(chan struct{})
	var mu sync.Mutex
	seen := map[string]int{}

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			local := map[string]int{}
			for j := 0; j < picksEach; j++ {
				picked, ok := sel.PickPrimary(states)
				if !ok {
					t.Error("PickPrimary must always select from healthy gateways")
					return
				}
				local[picked.State.ID]++
			}
			mu.Lock()
			for id, n := range local {
				seen[id] += n
			}
			mu.Unlock()
		}()
	}

	close(start)
	wg.Wait()

	total := 0
	for _, n := range seen {
		total += n
	}
	if total != goroutines*picksEach {
		t.Fatalf("picks: got %d, want %d", total, goroutines*picksEach)
	}
	// A source stuck on one value would still be race-free but useless for
	// load spreading, so require every healthy gateway to receive traffic.
	if len(seen) != len(states) {
		t.Fatalf("weighted selection reached %d of %d gateways: %v", len(seen), len(states), seen)
	}
}

// FailoverOrder shares the same Selector and must stay read-only.
func TestSelector_FailoverOrderIsConcurrencySafe(t *testing.T) {
	cfg := DefaultConfig()
	sel := NewSelector(cfg)
	states := []GatewayState{
		{Gateway: Gateway{ID: "a", Weight: 100}, Enabled: true, HealthScore: 95, LatencyEMAMs: 10, SuccessCount: 100},
		{Gateway: Gateway{ID: "b", Weight: 50}, Enabled: true, HealthScore: 80, LatencyEMAMs: 50, SuccessCount: 100},
	}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = sel.FailoverOrder(states, "a")
				_, _ = sel.PickPrimary(states)
			}
		}()
	}
	wg.Wait()
}
