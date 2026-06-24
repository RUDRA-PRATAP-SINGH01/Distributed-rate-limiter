package routing

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func setupRoutingStore(t *testing.T) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return NewRedisStore(rdb, DefaultConfig()), mr
}

func TestRegisterAndRecordOutcome(t *testing.T) {
	store, _ := setupRoutingStore(t)
	ctx := context.Background()

	if err := store.RegisterGateway(ctx, Gateway{ID: "gateway-a", URL: "http://a:8081", Weight: 100}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 20; i++ {
		_ = store.RecordOutcome(ctx, Outcome{
			GatewayID: "gateway-a",
			Latency:   15 * time.Millisecond,
			Success:   true,
		})
	}
	for i := 0; i < 2; i++ {
		_ = store.RecordOutcome(ctx, Outcome{
			GatewayID: "gateway-a",
			Latency:   15 * time.Millisecond,
			Success:   false,
		})
	}

	gw, err := store.GetGateway(ctx, "gateway-a")
	if err != nil || gw == nil {
		t.Fatalf("get gateway: %#v %v", gw, err)
	}
	if gw.SuccessCount < 20 || gw.ErrorCount < 2 {
		t.Fatalf("expected counts recorded: %+v", gw)
	}
	if gw.HealthScore <= 0 {
		t.Fatalf("expected positive health: %f", gw.HealthScore)
	}
}

func TestCircuitOpensOnHighErrors(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CircuitMinSamples = 5
	cfg.CircuitErrorRate = 0.5
	store, _ := setupRoutingStore(t)
	ctx := context.Background()
	_ = store.RegisterGateway(ctx, Gateway{ID: "gateway-c", URL: "http://c:8081", Weight: 60})

	for i := 0; i < 10; i++ {
		_ = store.RecordOutcome(ctx, Outcome{
			GatewayID: "gateway-c",
			Latency:   100 * time.Millisecond,
			Success:   false,
		})
	}

	gw, _ := store.GetGateway(ctx, "gateway-c")
	if gw == nil || !gw.CircuitOpen {
		t.Fatalf("expected circuit open, got %+v", gw)
	}
}
