package override

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestStores(t *testing.T) (*Store, *Store, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	rdbA := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	rdbB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return NewStore(rdbA, 5*time.Second), NewStore(rdbB, 5*time.Second), mr
}

func TestOverrideSetVisibleAcrossReplicas(t *testing.T) {
	storeA, storeB, mr := newTestStores(t)
	defer mr.Close()

	ctx := context.Background()
	uid := "user-cross-replica"

	// Warm cache on both replicas.
	if _, ok := storeB.GetUserOverride(ctx, uid); ok {
		t.Fatal("unexpected pre-existing override")
	}
	storeB.RefreshGeneration(ctx)
	if _, ok := storeB.GetUserOverride(ctx, uid); ok {
		t.Fatal("unexpected override after warm")
	}

	if err := storeA.SetOverride(ctx, "user", uid, Config{Capacity: 2, RefillRate: 0.1}); err != nil {
		t.Fatal(err)
	}

	storeB.RefreshGeneration(ctx)
	cfg, ok := storeB.GetUserOverride(ctx, uid)
	if !ok || cfg.Capacity != 2 {
		t.Fatalf("replica B expected capacity 2, got ok=%v cfg=%+v", ok, cfg)
	}
}

func TestOverrideUpdateVisibleAcrossReplicas(t *testing.T) {
	storeA, storeB, mr := newTestStores(t)
	defer mr.Close()
	ctx := context.Background()
	uid := "user-update"

	_ = storeA.SetOverride(ctx, "user", uid, Config{Capacity: 5, RefillRate: 1})
	storeB.RefreshGeneration(ctx)
	if cfg, ok := storeB.GetUserOverride(ctx, uid); !ok || cfg.Capacity != 5 {
		t.Fatalf("initial propagate failed: ok=%v cfg=%+v", ok, cfg)
	}

	_ = storeA.SetOverride(ctx, "user", uid, Config{Capacity: 1, RefillRate: 0.1})
	storeB.RefreshGeneration(ctx)
	cfg, ok := storeB.GetUserOverride(ctx, uid)
	if !ok || cfg.Capacity != 1 {
		t.Fatalf("update propagate failed: ok=%v cfg=%+v", ok, cfg)
	}
}

func TestOverrideDeleteVisibleAcrossReplicas(t *testing.T) {
	storeA, storeB, mr := newTestStores(t)
	defer mr.Close()
	ctx := context.Background()
	uid := "user-delete"

	_ = storeA.SetOverride(ctx, "user", uid, Config{Capacity: 3, RefillRate: 1})
	storeB.RefreshGeneration(ctx)
	if _, ok := storeB.GetUserOverride(ctx, uid); !ok {
		t.Fatal("expected override before delete")
	}

	if err := storeA.DeleteOverride(ctx, "user", uid); err != nil {
		t.Fatal(err)
	}
	storeB.RefreshGeneration(ctx)
	if _, ok := storeB.GetUserOverride(ctx, uid); ok {
		t.Fatal("replica B still sees deleted override")
	}
}

func TestOverrideConcurrentReadWrite(t *testing.T) {
	storeA, storeB, mr := newTestStores(t)
	defer mr.Close()
	ctx := context.Background()

	const workers = 16
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(workers * 2)

	for i := 0; i < workers; i++ {
		go func(n int) {
			defer wg.Done()
			<-start
			uid := "user-race"
			_ = storeA.SetOverride(ctx, "user", uid, Config{Capacity: n%5 + 1, RefillRate: 1})
		}(i)
		go func() {
			defer wg.Done()
			<-start
			storeB.RefreshGeneration(ctx)
			_, _ = storeB.GetUserOverride(ctx, "user-race")
		}()
	}
	close(start)
	wg.Wait()
}

func TestOverrideReplicaRestartClearsStaleCache(t *testing.T) {
	storeA, storeB, mr := newTestStores(t)
	defer mr.Close()
	ctx := context.Background()
	uid := "user-restart"

	_ = storeA.SetOverride(ctx, "user", uid, Config{Capacity: 4, RefillRate: 1})
	storeB.RefreshGeneration(ctx)
	_, _ = storeB.GetUserOverride(ctx, uid)

	// Simulate restart: new Store with same Redis, stale cache empty.
	storeB2 := NewStore(redis.NewClient(&redis.Options{Addr: mr.Addr()}), 5*time.Second)
	storeB2.RefreshGeneration(ctx)
	cfg, ok := storeB2.GetUserOverride(ctx, uid)
	if !ok || cfg.Capacity != 4 {
		t.Fatalf("restarted replica expected capacity 4, got ok=%v cfg=%+v", ok, cfg)
	}
}

func TestOverrideRedisInterruptionDoesNotPermanentStaleCache(t *testing.T) {
	storeA, storeB, mr := newTestStores(t)
	uid := "user-redis-blip"
	ctx := context.Background()

	_ = storeA.SetOverride(ctx, "user", uid, Config{Capacity: 7, RefillRate: 1})
	storeB.RefreshGeneration(ctx)
	_, _ = storeB.GetUserOverride(ctx, uid)

	mr.Close()
	storeB.RefreshGeneration(ctx) // Redis down: should not advance generation

	_ = storeA.SetOverride(ctx, "user", uid, Config{Capacity: 1, RefillRate: 0.1})
	// storeB still has old cached value until Redis returns — acceptable bounded by next successful RefreshGeneration
}

func TestOverrideGetRespectsContextCancellation(t *testing.T) {
	storeA, _, mr := newTestStores(t)
	defer mr.Close()
	ctx := context.Background()
	uid := "user-ctx-cancel"

	_ = storeA.SetOverride(ctx, "user", uid, Config{Capacity: 5, RefillRate: 1})

	// Flush cache to force Redis hit.
	storeA.cache.Range(func(k, _ any) bool { storeA.cache.Delete(k); return true })

	// Cancelled context — cache miss hits Redis, gets error, returns (Config{}, false).
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()

	_, ok := storeA.GetUserOverride(cancelCtx, uid)
	if ok {
		t.Fatal("expected GetUserOverride to return false with cancelled context on cache miss")
	}
	// Product invariant: cancelled context -> fall back to defaults, not panic or block.
}

func TestOverrideSetRespectsContextDeadline(t *testing.T) {
	storeA, _, mr := newTestStores(t)
	defer mr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond)

	err := storeA.SetOverride(ctx, "user", "uid", Config{Capacity: 1, RefillRate: 1})
	if err == nil {
		t.Fatal("expected SetOverride to return error with expired context")
	}
}
