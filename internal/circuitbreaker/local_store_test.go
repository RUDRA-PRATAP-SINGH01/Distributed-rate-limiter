package circuitbreaker

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestLocalStore_ClosedToOpenOnConsecutiveFailures(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinSamples = 2
	cfg.ConsecutiveFailures = 2
	cfg.FailureRateThreshold = 0.99 // force consecutive path
	b := NewBreaker(NewLocalStore(cfg))
	ctx := context.Background()
	target := TargetRedis

	for i := 0; i < 2; i++ {
		allow, err := b.Allow(ctx, target)
		if err != nil || !allow.Allowed {
			t.Fatalf("iter %d: expected allowed, got %+v err=%v", i, allow, err)
		}
		if err := b.Record(ctx, target, RecordInput{Kind: OutcomeFailure, Latency: 10 * time.Millisecond}); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	st, err := b.GetState(ctx, target)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if st.State != StateOpen {
		t.Fatalf("expected open, got %s (%+v)", st.State, st)
	}

	allow, err := b.Allow(ctx, target)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if allow.Allowed || allow.State != StateOpen {
		t.Fatalf("open circuit should block, got %+v", allow)
	}
}

func TestLocalStore_OpenToHalfOpenToClosed(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinSamples = 1
	cfg.ConsecutiveFailures = 1
	cfg.OpenCooldownMs = 50
	cfg.HalfOpenMaxProbes = 2
	cfg.HalfOpenSuccessRequired = 2
	b := NewBreaker(NewLocalStore(cfg))
	ctx := context.Background()
	target := TargetRedis

	_, _ = b.Allow(ctx, target)
	_ = b.Record(ctx, target, RecordInput{Kind: OutcomeFailure, Latency: 5 * time.Millisecond})

	st, _ := b.GetState(ctx, target)
	if st.State != StateOpen {
		t.Fatalf("expected open, got %s", st.State)
	}

	time.Sleep(60 * time.Millisecond)

	allow, err := b.Allow(ctx, target)
	if err != nil || !allow.Allowed || allow.State != StateHalfOpen {
		t.Fatalf("expected half-open probe, got %+v err=%v", allow, err)
	}
	_ = b.Record(ctx, target, RecordInput{Kind: OutcomeSuccess, Latency: 1 * time.Millisecond})

	allow2, _ := b.Allow(ctx, target)
	if !allow2.Allowed {
		t.Fatal("second half-open probe should be allowed")
	}
	_ = b.Record(ctx, target, RecordInput{Kind: OutcomeSuccess, Latency: 1 * time.Millisecond})

	st2, _ := b.GetState(ctx, target)
	if st2.State != StateClosed {
		t.Fatalf("expected closed after half-open successes, got %s", st2.State)
	}
}

func TestLocalStore_AllowNeverErrors(t *testing.T) {
	b := NewBreaker(NewLocalStore(DefaultConfig()))
	ctx := context.Background()
	_, err := b.Allow(ctx, TargetRedis)
	if err != nil {
		t.Fatalf("LocalStore Allow must not error: %v", err)
	}
	if err := b.Record(ctx, TargetRedis, RecordInput{Kind: OutcomeTimeout, Latency: time.Second}); err != nil {
		t.Fatalf("LocalStore Record must not error: %v", err)
	}
}

func TestLocalStore_ConcurrentRecord(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ConsecutiveFailures = 50
	cfg.MinSamples = 50
	b := NewBreaker(NewLocalStore(cfg))
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = b.Allow(ctx, TargetRedis)
			_ = b.Record(ctx, TargetRedis, RecordInput{Kind: OutcomeFailure, Latency: time.Millisecond})
		}()
	}
	wg.Wait()

	st, err := b.GetState(ctx, TargetRedis)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if st.TotalCount == 0 {
		t.Fatal("expected non-zero total count")
	}
}

func TestLocalStore_HalfOpenProbeExhaustionPreservesHalfOpenUntilOutcomeOrTimeout(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ConsecutiveFailures = 1
	cfg.OpenCooldownMs = 50
	cfg.HalfOpenMaxProbes = 2
	b := NewBreaker(NewLocalStore(cfg))
	ctx := context.Background()
	target := TargetRedis

	_, _ = b.Allow(ctx, target)
	_ = b.Record(ctx, target, RecordInput{Kind: OutcomeFailure, Latency: time.Millisecond})

	st, _ := b.GetState(ctx, target)
	if st.State != StateOpen {
		t.Fatalf("expected open, got %s", st.State)
	}

	time.Sleep(60 * time.Millisecond)

	for i := int64(0); i < cfg.HalfOpenMaxProbes; i++ {
		allow, err := b.Allow(ctx, target)
		if err != nil || !allow.Allowed || allow.State != StateHalfOpen {
			t.Fatalf("probe %d: expected allowed half-open, got %+v %v", i, allow, err)
		}
	}

	// Next call must be rejected because probe quota is in-flight, but stay half-open
	allow, _ := b.Allow(ctx, target)
	if allow.Allowed {
		t.Fatal("expected rejection when probe budget is reached")
	}
	st, _ = b.GetState(ctx, target)
	if st.State != StateHalfOpen {
		t.Fatalf("expected state to remain half_open while probes in flight, got %s", st.State)
	}

	// Wait for cooldown timeout to expire without recovery
	time.Sleep(time.Duration(cfg.OpenCooldownMs+20) * time.Millisecond)
	allowAfterTimeout, _ := b.Allow(ctx, target)
	if allowAfterTimeout.Allowed {
		t.Fatal("expected rejection after timeout deadline")
	}
	stAfter, _ := b.GetState(ctx, target)
	if stAfter.State != StateOpen {
		t.Fatalf("expected state to transition to open after timeout deadline, got %s", stAfter.State)
	}
}
