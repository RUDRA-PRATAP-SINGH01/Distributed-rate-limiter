package circuitbreaker

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/redistest"
)

func setupCB(t *testing.T) (*Breaker, *redistest.Server) {
	t.Helper()
	srv := redistest.Start(t)
	cfg := DefaultConfig()
	cfg.MinSamples = 5
	cfg.ConsecutiveFailures = 6
	cfg.FailureRateThreshold = 0.5
	cfg.OpenCooldownMs = 100
	cfg.HalfOpenMaxProbes = 2
	cfg.HalfOpenSuccessRequired = 2
	return NewBreaker(NewRedisStore(srv.Client(t), cfg)), srv
}

func TestClosedToOpenOnFailures(t *testing.T) {
	b, _ := setupCB(t)
	ctx := context.Background()
	target := "gateway-c"

	for i := 0; i < 5; i++ {
		allow, err := b.Allow(ctx, target)
		if err != nil || !allow.Allowed {
			t.Fatalf("iter %d: expected allowed, got %+v %v", i, allow, err)
		}
		_ = b.Record(ctx, target, RecordInput{Kind: OutcomeFailure, Latency: 50 * time.Millisecond})
	}

	st, _ := b.GetState(ctx, target)
	if st.State != StateOpen {
		t.Fatalf("expected open, got %s (%+v)", st.State, st)
	}

	allow, _ := b.Allow(ctx, target)
	if allow.Allowed {
		t.Fatal("open circuit should block traffic")
	}
}

func TestOpenToHalfOpenToClosed(t *testing.T) {
	b, _ := setupCB(t)
	ctx := context.Background()
	target := "redis"

	for i := 0; i < 6; i++ {
		_, _ = b.Allow(ctx, target)
		_ = b.Record(ctx, target, RecordInput{Kind: OutcomeFailure, Latency: 10 * time.Millisecond})
	}
	st, _ := b.GetState(ctx, target)
	if st.State != StateOpen {
		t.Fatalf("expected open, got %s", st.State)
	}

	time.Sleep(150 * time.Millisecond)

	allow, _ := b.Allow(ctx, target)
	if !allow.Allowed || allow.State != StateHalfOpen {
		t.Fatalf("expected half-open probe allowed, got %+v", allow)
	}
	_ = b.Record(ctx, target, RecordInput{Kind: OutcomeSuccess, Latency: 5 * time.Millisecond})

	allow2, _ := b.Allow(ctx, target)
	if !allow2.Allowed {
		t.Fatal("second half-open probe should be allowed")
	}
	_ = b.Record(ctx, target, RecordInput{Kind: OutcomeSuccess, Latency: 5 * time.Millisecond})

	st, _ = b.GetState(ctx, target)
	if st.State != StateClosed {
		t.Fatalf("expected closed after successful probes, got %s", st.State)
	}
}

func TestHalfOpenFailureReopens(t *testing.T) {
	b, _ := setupCB(t)
	ctx := context.Background()
	target := "gw"

	for i := 0; i < 6; i++ {
		_, _ = b.Allow(ctx, target)
		_ = b.Record(ctx, target, RecordInput{Kind: OutcomeFailure, Latency: 1 * time.Millisecond})
	}
	time.Sleep(150 * time.Millisecond)

	_, _ = b.Allow(ctx, target)
	_ = b.Record(ctx, target, RecordInput{Kind: OutcomeFailure, Latency: 1 * time.Millisecond})

	st, _ := b.GetState(ctx, target)
	if st.State != StateOpen {
		t.Fatalf("expected reopened, got %s", st.State)
	}
}

func TestTimeoutTripsCircuit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinSamples = 3
	cfg.TimeoutRateThreshold = 0.3
	cfg.ConsecutiveFailures = 100
	b := NewBreaker(NewRedisStore(redistest.Start(t).Client(t), cfg))
	ctx := context.Background()
	target := "limiter"

	for i := 0; i < 4; i++ {
		_, _ = b.Allow(ctx, target)
		_ = b.Record(ctx, target, RecordInput{Kind: OutcomeTimeout, Latency: 5 * time.Second})
	}
	st, _ := b.GetState(ctx, target)
	if st.State != StateOpen {
		t.Fatalf("expected open on timeouts, got %s (rate=%.2f)", st.State, st.TimeoutRate)
	}
}

func TestHalfOpenProbeExhaustionTransitionsToOpen(t *testing.T) {
	b, _ := setupCB(t)
	ctx := context.Background()
	target := "gw-exhaust"

	for i := 0; i < 6; i++ {
		_, _ = b.Allow(ctx, target)
		_ = b.Record(ctx, target, RecordInput{Kind: OutcomeFailure, Latency: 1 * time.Millisecond})
	}
	time.Sleep(150 * time.Millisecond)

	cfg := b.Config()
	for i := int64(0); i < cfg.HalfOpenMaxProbes; i++ {
		allow, err := b.Allow(ctx, target)
		if err != nil || !allow.Allowed {
			t.Fatalf("probe %d: expected allowed in half-open, got %+v %v", i, allow, err)
		}
		// Do not record — probes consumed without outcomes.
	}

	allow, _ := b.Allow(ctx, target)
	if allow.Allowed {
		t.Fatal("expected rejection after probe budget exhausted")
	}
	st, _ := b.GetState(ctx, target)
	if st.State != StateOpen {
		t.Fatalf("expected transition back to open, got %s", st.State)
	}
}

func TestHalfOpenConcurrentProbeBound(t *testing.T) {
	b, _ := setupCB(t)
	ctx := context.Background()
	target := "gw-concurrent"

	for i := 0; i < 6; i++ {
		_, _ = b.Allow(ctx, target)
		_ = b.Record(ctx, target, RecordInput{Kind: OutcomeFailure, Latency: 1 * time.Millisecond})
	}
	time.Sleep(150 * time.Millisecond)

	cfg := b.Config()
	maxProbes := cfg.HalfOpenMaxProbes
	const workers = 32
	start := make(chan struct{})
	var (
		wg       sync.WaitGroup
		admitted atomic.Int64
		rejected atomic.Int64
	)
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			<-start
			allow, err := b.Allow(ctx, target)
			if err != nil {
				t.Errorf("allow: %v", err)
				return
			}
			if allow.Allowed {
				admitted.Add(1)
			} else {
				rejected.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := admitted.Load(); got > maxProbes {
		t.Fatalf("admitted %d probes exceeds HalfOpenMaxProbes=%d", got, maxProbes)
	}
	if got := admitted.Load(); got == 0 {
		t.Fatal("expected at least one admitted half-open probe")
	}
	st, _ := b.GetState(ctx, target)
	if st.HalfOpenCalls > maxProbes {
		t.Fatalf("redis half_open_calls=%d exceeds max=%d", st.HalfOpenCalls, maxProbes)
	}
}

func TestReset(t *testing.T) {
	b, _ := setupCB(t)
	ctx := context.Background()
	target := "x"
	for i := 0; i < 6; i++ {
		_, _ = b.Allow(ctx, target)
		_ = b.Record(ctx, target, RecordInput{Kind: OutcomeFailure, Latency: 1 * time.Millisecond})
	}
	if err := b.Reset(ctx, target); err != nil {
		t.Fatal(err)
	}
	st, _ := b.GetState(ctx, target)
	if st.State != StateClosed || st.TotalCount != 0 {
		t.Fatalf("expected clean closed state, got %+v", st)
	}
}
