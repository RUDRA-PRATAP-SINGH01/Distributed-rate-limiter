package idempotency

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/metrics"
	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/telemetry"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
)

//go:embed lua/claim.lua
var claimLua string

//go:embed lua/complete.lua
var completeLua string

// RedisStore is the production idempotency backend.
type RedisStore struct {
	rdb           *redis.Client
	cfg           Config
	claimScript   *redis.Script
	completeScript *redis.Script
}

func NewRedisStore(rdb *redis.Client, cfg Config) *RedisStore {
	return &RedisStore{
		rdb:            rdb,
		cfg:            cfg,
		claimScript:    redis.NewScript(claimLua),
		completeScript: redis.NewScript(completeLua),
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

	ctx, span := telemetry.StartSpan(ctx, "idempotency.claim",
		attribute.String("idempotency.key", key),
	)
	defer span.End()

	start := time.Now()
	result, err := s.claimScript.Run(ctx, s.rdb,
		[]string{metaKey(scope, key), bodyKey(scope, key)},
		requestHash,
		NowMillis(),
		s.cfg.LockTTL,
		s.cfg.CompletedTTL,
	).Result()
	metrics.RecordIdempotencyRedisDuration(time.Since(start).Seconds())

	if err != nil {
		metrics.RecordIdempotencyClaim("error")
		return nil, fmt.Errorf("%w: %v", ErrStoreUnavailable, err)
	}

	values, ok := result.([]interface{})
	if !ok || len(values) < 1 {
		metrics.RecordIdempotencyClaim("error")
		return nil, fmt.Errorf("%w: unexpected lua result", ErrStoreUnavailable)
	}

	code := luaInt(values[0])
	switch code {
	case 1:
		metrics.RecordIdempotencyClaim("claimed")
		return &ClaimResponse{Result: ResultClaimed}, nil
	case 2:
		metrics.RecordIdempotencyClaim("replay")
		status := int(luaInt(values[1]))
		headers := decodeHeaders(luaString(values[2]))
		body := []byte(luaString(values[3]))
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
			retryMs = luaInt(values[1])
		}
		return &ClaimResponse{Result: ResultInProgress, RetryAfterMs: retryMs}, nil
	case 0:
		metrics.RecordIdempotencyClaim("hash_mismatch")
		return &ClaimResponse{Result: ResultHashMismatch}, nil
	default:
		metrics.RecordIdempotencyClaim("error")
		return nil, fmt.Errorf("%w: unknown claim code %d", ErrStoreUnavailable, code)
	}
}

func (s *RedisStore) Complete(ctx context.Context, req CompleteRequest) error {
	if int64(len(req.Body)) > s.cfg.MaxBodyBytes {
		return ErrBodyTooLarge
	}

	ctx, span := telemetry.StartSpan(ctx, "idempotency.complete",
		attribute.String("idempotency.key", req.Key),
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
	).Result()
	metrics.RecordIdempotencyRedisDuration(time.Since(start).Seconds())

	if err != nil {
		return fmt.Errorf("%w: %v", ErrStoreUnavailable, err)
	}

	values, ok := result.([]interface{})
	if !ok || len(values) < 1 || luaInt(values[0]) != 1 {
		return fmt.Errorf("%w: complete transition failed", ErrStoreUnavailable)
	}

	metrics.RecordIdempotencyComplete()
	return nil
}

func luaInt(v interface{}) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case string:
		var parsed int64
		fmt.Sscan(n, &parsed)
		return parsed
	default:
		return 0
	}
}

func luaString(v interface{}) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return fmt.Sprint(v)
	}
}

// ReadBody reads and restores the request body for fingerprinting and proxying.
func ReadBody(r *http.Request, maxBytes int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
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
