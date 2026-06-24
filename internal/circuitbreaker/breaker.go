package circuitbreaker

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// Breaker is the high-level circuit breaker API used by services.
type Breaker struct {
	store *RedisStore
	cfg   Config
}

func NewBreaker(store *RedisStore) *Breaker {
	return &Breaker{store: store, cfg: store.cfg}
}

func (b *Breaker) Config() Config { return b.cfg }

func (b *Breaker) Store() *RedisStore { return b.store }

func (b *Breaker) Allow(ctx context.Context, target string) (AllowResult, error) {
	return b.store.Allow(ctx, target)
}

func (b *Breaker) Record(ctx context.Context, target string, input RecordInput) error {
	_, err := b.store.Record(ctx, target, input)
	return err
}

func (b *Breaker) GetState(ctx context.Context, target string) (Snapshot, error) {
	return b.store.GetState(ctx, target)
}

func (b *Breaker) Reset(ctx context.Context, target string) error {
	return b.store.Reset(ctx, target)
}

func (b *Breaker) List(ctx context.Context) ([]Snapshot, error) {
	return b.store.ListTargets(ctx)
}

// ClassifyHTTP maps an HTTP upstream result to an outcome kind.
func ClassifyHTTP(err error, statusCode int, latency time.Duration, latencyThresholdMs int64) RecordInput {
	if err != nil {
		var netErr net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
			return RecordInput{Kind: OutcomeTimeout, Latency: latency}
		}
		return RecordInput{Kind: OutcomeFailure, Latency: latency}
	}
	if statusCode >= http.StatusInternalServerError {
		return RecordInput{Kind: OutcomeFailure, Latency: latency}
	}
	if latencyThresholdMs > 0 && latency.Milliseconds() >= latencyThresholdMs {
		return RecordInput{Kind: OutcomeLatencySpike, Latency: latency}
	}
	return RecordInput{Kind: OutcomeSuccess, Latency: latency}
}

// ClassifyError maps a dependency error (e.g. Redis) to an outcome kind.
func ClassifyError(err error, latency time.Duration, latencyThresholdMs int64) RecordInput {
	if err == nil {
		if latencyThresholdMs > 0 && latency.Milliseconds() >= latencyThresholdMs {
			return RecordInput{Kind: OutcomeLatencySpike, Latency: latency}
		}
		return RecordInput{Kind: OutcomeSuccess, Latency: latency}
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return RecordInput{Kind: OutcomeTimeout, Latency: latency}
	}
	return RecordInput{Kind: OutcomeFailure, Latency: latency}
}
