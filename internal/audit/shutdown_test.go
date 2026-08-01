package audit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/redistest"
	"github.com/redis/go-redis/v9"
)

func newAsyncStore(t *testing.T, workers, queueSize int) (*Store, *redistest.Server, redis.UniversalClient) {
	t.Helper()
	srv := redistest.Start(t)
	cfg := DefaultConfig()
	cfg.Async = true
	cfg.Workers = workers
	cfg.QueueSize = queueSize
	cfg.Retention = time.Hour
	// Tight timeouts keep the shutdown-budget assertions meaningful when Redis
	// stops answering.
	rdb := redis.NewClient(&redis.Options{
		Addr:         srv.Addr(),
		DialTimeout:  100 * time.Millisecond,
		ReadTimeout:  100 * time.Millisecond,
		WriteTimeout: 100 * time.Millisecond,
		MaxRetries:   -1,
	})
	return NewStore(rdb, cfg), srv, rdb
}

func sampleInput(i int) RecordInput {
	return RecordInput{
		RequestID: fmt.Sprintf("req-%d", i),
		UserID:    "user-1",
		Decision:  DecisionAllowed,
		Reason:    "check: quota available",
		Handler:   "check",
		Remaining: 9,
	}
}

func TestShutdown_EmptyQueue(t *testing.T) {
	store, _, rdb := newAsyncStore(t, 2, 8)
	defer rdb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	if err := store.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("expected prompt shutdown, took %v", elapsed)
	}
	if !store.RedisCloseSafe() {
		t.Fatal("expected RedisCloseSafe after shutdown")
	}
}

func TestShutdown_DrainsPendingEvents(t *testing.T) {
	store, _, rdb := newAsyncStore(t, 1, 32)
	defer rdb.Close()

	ctx := context.Background()
	const n = 8
	for i := 0; i < n; i++ {
		if _, err := store.Record(ctx, sampleInput(i)); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := store.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats["events_indexed"] != int64(n) {
		t.Fatalf("expected %d indexed events, got %d", n, stats["events_indexed"])
	}
}

func TestShutdown_ConcurrentRecordNoPanic(t *testing.T) {
	store, _, rdb := newAsyncStore(t, 4, 256)
	defer rdb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var producers sync.WaitGroup
	stop := make(chan struct{})
	for p := 0; p < 8; p++ {
		producers.Add(1)
		go func(id int) {
			defer producers.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
					_, _ = store.Record(ctx, sampleInput(id*1000+i))
				}
			}
		}(p)
	}

	time.Sleep(20 * time.Millisecond)
	errCh := make(chan error, 1)
	go func() {
		errCh <- store.Shutdown(ctx)
	}()

	time.Sleep(30 * time.Millisecond)
	close(stop)
	producers.Wait()

	if err := <-errCh; err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if !store.RedisCloseSafe() {
		t.Fatal("workers still active")
	}
}

func TestShutdown_RedisUnavailableBounded(t *testing.T) {
	store, srv, rdb := newAsyncStore(t, 1, 4)
	srv.SkipIfReal(t, "kills the server to make queued writes hang")

	ctx := context.Background()
	if _, err := store.Record(ctx, sampleInput(0)); err != nil {
		t.Fatalf("record: %v", err)
	}
	srv.Stop(t)

	shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	start := time.Now()
	err := store.Shutdown(shutdownCtx)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("expected worker to finish after redis error, got %v after %v", err, elapsed)
	}
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("shutdown blocked too long: %v", elapsed)
	}
	if !store.RedisCloseSafe() {
		t.Fatal("expected workers terminated")
	}
	_ = rdb.Close()
}

func TestShutdown_RedisCloseOrdering(t *testing.T) {
	store, _, rdb := newAsyncStore(t, 1, 4)

	ctx := context.Background()
	if _, err := store.Record(ctx, sampleInput(0)); err != nil {
		t.Fatalf("record: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := store.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if !store.RedisCloseSafe() {
		t.Fatal("audit workers still active before redis close")
	}

	var redisOps atomic.Int32
	store.beforeRecord = func() { redisOps.Add(1) }
	if err := rdb.Close(); err != nil {
		t.Fatalf("redis close: %v", err)
	}
	// The record is dropped after shutdown; either outcome is acceptable as
	// long as it never reaches Redis, which the counter below asserts.
	_, _ = store.Record(ctx, sampleInput(1))
	if redisOps.Load() != 0 {
		t.Fatalf("record after shutdown should not execute redis, ops=%d", redisOps.Load())
	}
}

func TestShutdown_Idempotent(t *testing.T) {
	store, _, rdb := newAsyncStore(t, 2, 8)
	defer rdb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = store.Shutdown(ctx)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("shutdown %d: %v", i, err)
		}
	}
	if !store.RedisCloseSafe() {
		t.Fatal("expected stopped state")
	}
}

func TestShutdown_RecordAfterShutdownDropped(t *testing.T) {
	store, _, rdb := newAsyncStore(t, 1, 4)
	defer rdb.Close()

	ctx := context.Background()
	if err := store.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	ev, err := store.Record(ctx, sampleInput(0))
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if ev.RequestID == "" {
		t.Fatal("expected synthetic event without error")
	}
	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats["events_indexed"] != 0 {
		t.Fatalf("expected no persisted events after shutdown drop, got %d", stats["events_indexed"])
	}
}

func TestShutdown_TimeoutThenResume(t *testing.T) {
	store, _, rdb := newAsyncStore(t, 1, 4)
	defer rdb.Close()

	block := make(chan struct{})
	store.beforeRecord = func() {
		<-block
	}

	ctx := context.Background()
	if _, err := store.Record(ctx, sampleInput(0)); err != nil {
		t.Fatalf("record: %v", err)
	}

	shortCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	err := store.Shutdown(shortCtx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if store.RedisCloseSafe() {
		t.Fatal("workers should still be active after timeout")
	}

	close(block)

	longCtx, cancel2 := context.WithTimeout(ctx, 2*time.Second)
	defer cancel2()
	if err := store.Shutdown(longCtx); err != nil {
		t.Fatalf("resume shutdown: %v", err)
	}
	if !store.RedisCloseSafe() {
		t.Fatal("expected workers terminated after resume")
	}
}

func TestShutdown_HighContentionRaceStress(t *testing.T) {
	for round := 0; round < 5; round++ {
		store, srv, rdb := newAsyncStore(t, 4, 128)
		srv.SkipIfReal(t, "tears the server down between rounds")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)

		var wg sync.WaitGroup
		stop := make(chan struct{})
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				for j := 0; ; j++ {
					select {
					case <-stop:
						return
					default:
						_, _ = store.Record(ctx, sampleInput(n*1000+j))
					}
				}
			}(i)
		}

		time.Sleep(10 * time.Millisecond)
		err := store.Shutdown(ctx)
		close(stop)
		wg.Wait()
		cancel()
		srv.Stop(t)
		rdb.Close()

		if err != nil {
			t.Fatalf("round %d shutdown: %v", round, err)
		}
		if !store.RedisCloseSafe() {
			t.Fatalf("round %d workers still active", round)
		}
	}
}
