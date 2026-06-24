package idempotency

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func setupTestStore(t *testing.T) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	cfg := DefaultConfig()
	cfg.LockTTL = 5_000
	cfg.CompletedTTL = 60_000
	return NewRedisStore(rdb, cfg), mr
}

func TestClaimSingleWinnerUnderConcurrency(t *testing.T) {
	store, _ := setupTestStore(t)
	const n = 100
	var claimed, inProgress, other int32
	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			resp, err := store.Claim(context.Background(), "scope1", "key-race", "hash-abc")
			if err != nil {
				atomic.AddInt32(&other, 1)
				return
			}
			switch resp.Result {
			case ResultClaimed:
				atomic.AddInt32(&claimed, 1)
			case ResultInProgress:
				atomic.AddInt32(&inProgress, 1)
			default:
				atomic.AddInt32(&other, 1)
			}
		}()
	}
	wg.Wait()

	if claimed != 1 {
		t.Fatalf("expected exactly 1 claim, got %d (in_progress=%d other=%d)", claimed, inProgress, other)
	}
	if inProgress != 99 {
		t.Fatalf("expected 99 in_progress, got %d", inProgress)
	}
}

func TestCompleteAndReplay(t *testing.T) {
	store, _ := setupTestStore(t)
	ctx := context.Background()

	claim, err := store.Claim(ctx, "scope1", "key-replay", "hash-1")
	if err != nil || claim.Result != ResultClaimed {
		t.Fatalf("claim failed: %#v %v", claim, err)
	}

	err = store.Complete(ctx, CompleteRequest{
		Scope:      "scope1",
		Key:        "key-replay",
		HTTPStatus: 201,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       []byte(`{"order_id":"ord_123"}`),
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	replay, err := store.Claim(ctx, "scope1", "key-replay", "hash-1")
	if err != nil {
		t.Fatalf("replay claim: %v", err)
	}
	if replay.Result != ResultReplay {
		t.Fatalf("expected replay, got %v", replay.Result)
	}
	if replay.HTTPStatus != 201 {
		t.Fatalf("expected status 201, got %d", replay.HTTPStatus)
	}
	if string(replay.Body) != `{"order_id":"ord_123"}` {
		t.Fatalf("unexpected body: %s", replay.Body)
	}
}

func TestHashMismatch(t *testing.T) {
	store, _ := setupTestStore(t)
	ctx := context.Background()

	_, err := store.Claim(ctx, "scope1", "key-mismatch", "hash-a")
	if err != nil {
		t.Fatal(err)
	}
	err = store.Complete(ctx, CompleteRequest{
		Scope: "scope1", Key: "key-mismatch", HTTPStatus: 200, Body: []byte("ok"),
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := store.Claim(ctx, "scope1", "key-mismatch", "hash-b")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Result != ResultHashMismatch {
		t.Fatalf("expected hash mismatch, got %v", resp.Result)
	}
}

func TestExpiredLockReclaim(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	cfg := DefaultConfig()
	cfg.LockTTL = 100
	cfg.CompletedTTL = 60_000
	store := NewRedisStore(rdb, cfg)
	ctx := context.Background()

	claim, err := store.Claim(ctx, "scope1", "key-expire", "hash-x")
	if err != nil || claim.Result != ResultClaimed {
		t.Fatalf("initial claim: %#v %v", claim, err)
	}

	mr.FastForward(200 * time.Millisecond)

	reclaim, err := store.Claim(ctx, "scope1", "key-expire", "hash-x")
	if err != nil {
		t.Fatal(err)
	}
	if reclaim.Result != ResultClaimed {
		t.Fatalf("expected reclaim (claimed), got %v", reclaim.Result)
	}
}

func TestFingerprintDeterministic(t *testing.T) {
	a := Fingerprint("POST", "/api/orders", []byte(`{"a":1}`))
	b := Fingerprint("POST", "/api/orders", []byte(`{"a":1}`))
	c := Fingerprint("POST", "/api/orders", []byte(`{"a":2}`))
	if a != b || a == c {
		t.Fatalf("fingerprint mismatch: a=%s b=%s c=%s", a, b, c)
	}
}
