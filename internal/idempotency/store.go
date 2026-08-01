package idempotency

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/luautil"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/metrics"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/telemetry"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
)

//go:embed lua/claim.lua
var claimLua string

//go:embed lua/complete.lua
var completeLua string

//go:embed lua/fail.lua
var failLua string

// RedisStore is the production idempotency backend.
type RedisStore struct {
	rdb            redis.UniversalClient
	cfg            Config
	claimScript    *redis.Script
	completeScript *redis.Script
	failScript     *redis.Script
}

func NewRedisStore(rdb redis.UniversalClient, cfg Config) *RedisStore {
	return &RedisStore{
		rdb:            rdb,
		cfg:            cfg,
		claimScript:    redis.NewScript(claimLua),
		completeScript: redis.NewScript(completeLua),
		failScript:     redis.NewScript(failLua),
	}
}

func metaKey(scope, key string) string {
	return fmt.Sprintf("idem:%s:%s", scope, key)
}

func bodyKey(scope, key string) string {
	return fmt.Sprintf("idem:body:%s:%s", scope, key)
}

func (s *RedisStore) Claim(ctx context.Context, scope, key, requestHash string) (*ClaimResponse, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}

	ctx, span := telemetry.StartSpan(ctx, "idempotency.claim")
	defer span.End()

	start := time.Now()
	fenceToken := uuid.New().String()
	result, err := s.claimScript.Run(ctx, s.rdb,
		[]string{metaKey(scope, key), bodyKey(scope, key)},
		requestHash,
		NowMillis(),
		s.cfg.LockTTL,
		s.cfg.CompletedTTL,
		fenceToken,
	).Result()
	metrics.RecordIdempotencyRedisDuration(time.Since(start).Seconds())

	if err != nil {
		metrics.RecordIdempotencyClaim("error")
		telemetry.RecordError(span, err)
		return nil, fmt.Errorf("%w: %w", ErrStoreUnavailable, err)
	}

	values, ok := result.([]interface{})
	if !ok || len(values) < 1 {
		metrics.RecordIdempotencyClaim("error")
		err := fmt.Errorf("%w: unexpected lua result", ErrStoreUnavailable)
		telemetry.RecordError(span, err)
		return nil, err
	}

	code := luautil.LuaInt(values[0])
	switch code {
	case 1:
		metrics.RecordIdempotencyClaim("claimed")
		token := fenceToken
		if len(values) > 1 {
			token = luautil.LuaString(values[1])
		}
		return &ClaimResponse{Result: ResultClaimed, FenceToken: token}, nil
	case 2:
		metrics.RecordIdempotencyClaim("replay")
		status := int(luautil.LuaInt(values[1]))
		headers := decodeHeaders(luautil.LuaString(values[2]))
		body := []byte(luautil.LuaString(values[3]))
		return &ClaimResponse{
			Result:     ResultReplay,
			HTTPStatus: status,
			Headers:    headers,
			Body:       body,
		}, nil
	case 3:
		metrics.RecordIdempotencyClaim("in_progress")
		retryMs := int64(0)
		if len(values) > 1 {
			retryMs = luautil.LuaInt(values[1])
		}
		return &ClaimResponse{Result: ResultInProgress, RetryAfterMs: retryMs}, nil
	case 0:
		metrics.RecordIdempotencyClaim("hash_mismatch")
		return &ClaimResponse{Result: ResultHashMismatch}, nil
	default:
		metrics.RecordIdempotencyClaim("error")
		err := fmt.Errorf("%w: unknown claim code %d", ErrStoreUnavailable, code)
		telemetry.RecordError(span, err)
		return nil, err
	}
}

func (s *RedisStore) Complete(ctx context.Context, req CompleteRequest) error {
	if int64(len(req.Body)) > s.cfg.MaxBodyBytes {
		return ErrBodyTooLarge
	}

	ctx, span := telemetry.StartSpan(ctx, "idempotency.complete",
		attribute.Int("http.status_code", req.HTTPStatus),
	)
	defer span.End()

	start := time.Now()
	result, err := s.completeScript.Run(ctx, s.rdb,
		[]string{metaKey(req.Scope, req.Key), bodyKey(req.Scope, req.Key)},
		req.HTTPStatus,
		encodeHeaders(req.Headers),
		string(req.Body),
		s.cfg.CompletedTTL,
		NowMillis(),
		s.cfg.InlineThreshold,
		req.FenceToken,
	).Result()
	metrics.RecordIdempotencyRedisDuration(time.Since(start).Seconds())

	if err != nil {
		telemetry.RecordError(span, err)
		return fmt.Errorf("%w: %w", ErrStoreUnavailable, err)
	}

	values, ok := result.([]interface{})
	if !ok || len(values) < 1 || luautil.LuaInt(values[0]) != 1 {
		telemetry.RecordError(span, ErrStaleFence)
		return ErrStaleFence
	}

	metrics.RecordIdempotencyComplete()
	return nil
}

func (s *RedisStore) Fail(ctx context.Context, req FailRequest) error {
	if int64(len(req.Body)) > s.cfg.MaxBodyBytes {
		return ErrBodyTooLarge
	}

	ctx, span := telemetry.StartSpan(ctx, "idempotency.fail",
		attribute.Int("http.status_code", req.HTTPStatus),
	)
	defer span.End()

	start := time.Now()
	result, err := s.failScript.Run(ctx, s.rdb,
		[]string{metaKey(req.Scope, req.Key)},
		req.HTTPStatus,
		encodeHeaders(req.Headers),
		string(req.Body),
		s.cfg.CompletedTTL,
		NowMillis(),
		req.FenceToken,
	).Result()
	metrics.RecordIdempotencyRedisDuration(time.Since(start).Seconds())

	if err != nil {
		telemetry.RecordError(span, err)
		return fmt.Errorf("%w: %w", ErrStoreUnavailable, err)
	}

	values, ok := result.([]interface{})
	if !ok || len(values) < 1 || luautil.LuaInt(values[0]) != 1 {
		telemetry.RecordError(span, ErrStaleFence)
		return ErrStaleFence
	}

	metrics.RecordIdempotencyClaim("failed")
	return nil
}

// ReadBody reads and restores the request body for fingerprinting and proxying.
func ReadBody(r *http.Request, maxBytes int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer func() { _ = r.Body.Close() }()
	limited := io.LimitReader(r.Body, maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, ErrBodyTooLarge
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}
