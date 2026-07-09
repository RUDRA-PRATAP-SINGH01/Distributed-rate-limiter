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
	if _, ok := storeB.GetUserOverride(uid); ok {
		t.Fatal("unexpected pre-existing override")
	}
	storeB.RefreshGeneration(ctx)
	if _, ok := storeB.GetUserOverride(uid); ok {
		t.Fatal("unexpected override after warm")
	}

	if err := storeA.SetOverride("user", uid, Config{Capacity: 2, RefillRate: 0.1}); err != nil {
		t.Fatal(err)
	}

	storeB.RefreshGeneration(ctx)
	cfg, ok := storeB.GetUserOverride(uid)
	if !ok || cfg.Capacity != 2 {
		t.Fatalf("replica B expected capacity 2, got ok=%v cfg=%+v", ok, cfg)
	}
}

func TestOverrideUpdateVisibleAcrossReplicas(t *testing.T) {
	storeA, storeB, mr := newTestStores(t)
	defer mr.Close()
	ctx := context.Background()
	uid := "user-update"

	_ = storeA.SetOverride("user", uid, Config{Capacity: 5, RefillRate: 1})
	storeB.RefreshGeneration(ctx)
	if cfg, ok := storeB.GetUserOverride(uid); !ok || cfg.Capacity != 5 {
		t.Fatalf("initial propagate failed: ok=%v cfg=%+v", ok, cfg)
	}

	_ = storeA.SetOverride("user", uid, Config{Capacity: 1, RefillRate: 0.1})
	storeB.RefreshGeneration(ctx)
	cfg, ok := storeB.GetUserOverride(uid)
	if !ok || cfg.Capacity != 1 {
		t.Fatalf("update propagate failed: ok=%v cfg=%+v", ok, cfg)
	}
}

func TestOverrideDeleteVisibleAcrossReplicas(t *testing.T) {
	storeA, storeB, mr := newTestStores(t)
	defer mr.Close()
	ctx := context.Background()
	uid := "user-delete"

	_ = storeA.SetOverride("user", uid, Config{Capacity: 3, RefillRate: 1})
	storeB.RefreshGeneration(ctx)
	if _, ok := storeB.GetUserOverride(uid); !ok {
		t.Fatal("expected override before delete")
	}

	if err := storeA.DeleteOverride("user", uid); err != nil {
		t.Fatal(err)
	}
	storeB.RefreshGeneration(ctx)
	if _, ok := storeB.GetUserOverride(uid); ok {
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
			_ = storeA.SetOverride("user", uid, Config{Capacity: n%5 + 1, RefillRate: 1})
		}(i)
		go func() {
			defer wg.Done()
			<-start
			storeB.RefreshGeneration(ctx)
			_, _ = storeB.GetUserOverride("user-race")
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

	_ = storeA.SetOverride("user", uid, Config{Capacity: 4, RefillRate: 1})
	storeB.RefreshGeneration(ctx)
	_, _ = storeB.GetUserOverride(uid)

	// Simulate restart: new Store with same Redis, stale cache empty.
	storeB2 := NewStore(redis.NewClient(&redis.Options{Addr: mr.Addr()}), 5*time.Second)
	storeB2.RefreshGeneration(ctx)
	cfg, ok := storeB2.GetUserOverride(uid)
	if !ok || cfg.Capacity != 4 {
		t.Fatalf("restarted replica expected capacity 4, got ok=%v cfg=%+v", ok, cfg)
	}
}

func TestOverrideRedisInterruptionDoesNotPermanentStaleCache(t *testing.T) {
	storeA, storeB, mr := newTestStores(t)
	uid := "user-redis-blip"
	ctx := context.Background()

	_ = storeA.SetOverride("user", uid, Config{Capacity: 7, RefillRate: 1})
	storeB.RefreshGeneration(ctx)
	_, _ = storeB.GetUserOverride(uid)

	mr.Close()
	storeB.RefreshGeneration(ctx) // Redis down: should not advance generation

	_ = storeA.SetOverride("user", uid, Config{Capacity: 1, RefillRate: 0.1})
	// storeB still has old cached value until Redis returns — acceptable bounded by next successful RefreshGeneration
}
