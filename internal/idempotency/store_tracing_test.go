package idempotency

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestCompleteStaleFenceRecordsSpanError(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))

	store, _ := setupTestStore(t)
	ctx := context.Background()

	claim, err := store.Claim(ctx, "scope1", "trace-stale-complete", "hash-1")
	if err != nil || claim.Result != ResultClaimed {
		t.Fatalf("claim failed: %#v %v", claim, err)
	}

	err = store.Complete(ctx, CompleteRequest{
		Scope:      "scope1",
		Key:        "trace-stale-complete",
		FenceToken: "wrong-fence-token",
		HTTPStatus: 201,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       []byte(`{"ok":true}`),
	})
	if !errors.Is(err, ErrStaleFence) {
		t.Fatalf("expected ErrStaleFence, got %v", err)
	}

	spans := sr.Ended()
	var found bool
	for _, span := range spans {
		if span.Name() != "idempotency.complete" {
			continue
		}
		found = true
		if span.Status().Code != codes.Error {
			t.Fatalf("expected error status on complete span, got %v", span.Status().Code)
		}
	}
	if !found {
		t.Fatal("idempotency.complete span not found")
	}
}

func TestFailRedisErrorRecordsSpanError(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))

	store, srv := setupTestStore(t)
	srv.SkipIfReal(t, "kills the server mid-test to force a Redis error")
	ctx := context.Background()

	claim, err := store.Claim(ctx, "scope1", "trace-fail-redis", "hash-1")
	if err != nil || claim.Result != ResultClaimed {
		t.Fatalf("claim failed: %#v %v", claim, err)
	}

	srv.Stop(t)

	err = store.Fail(ctx, FailRequest{
		Scope:      "scope1",
		Key:        "trace-fail-redis",
		FenceToken: claim.FenceToken,
		HTTPStatus: 503,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       []byte(`{"error":"upstream unavailable"}`),
	})
	if err == nil {
		t.Fatal("expected fail error when redis is closed")
	}

	spans := sr.Ended()
	var found bool
	for _, span := range spans {
		if span.Name() != "idempotency.fail" {
			continue
		}
		found = true
		if span.Status().Code != codes.Error {
			t.Fatalf("expected error status on fail span, got %v", span.Status().Code)
		}
	}
	if !found {
		t.Fatal("idempotency.fail span not found")
	}
}
