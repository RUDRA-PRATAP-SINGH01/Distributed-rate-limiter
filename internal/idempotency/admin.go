package idempotency

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// AdminRecord is a read-only view of an idempotency key for operators.
type AdminRecord struct {
	Scope       string            `json:"scope"`
	Key         string            `json:"key"`
	Status      Status            `json:"status"`
	RequestHash string            `json:"request_hash,omitempty"`
	HTTPStatus  int               `json:"http_status,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	BodySize    int               `json:"body_size"`
	BodyPreview string            `json:"body_preview,omitempty"`
	CreatedAt   int64             `json:"created_at_ms,omitempty"`
	CompletedAt int64             `json:"completed_at_ms,omitempty"`
	LockUntil   int64             `json:"lock_until_ms,omitempty"`
	TTLMs       int64             `json:"ttl_ms,omitempty"`
	BodyRef     string            `json:"body_ref,omitempty"`
}

const bodyPreviewMax = 256

// GetRecord returns metadata for an idempotency key (admin/debug).
func (s *RedisStore) GetRecord(ctx context.Context, scope, key string) (*AdminRecord, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}

	mk := metaKey(scope, key)
	fields, err := s.rdb.HGetAll(ctx, mk).Result()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStoreUnavailable, err)
	}
	if len(fields) == 0 {
		return nil, nil
	}

	ttl, err := s.rdb.PTTL(ctx, mk).Result()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStoreUnavailable, err)
	}

	rec := &AdminRecord{
		Scope:       scope,
		Key:         key,
		Status:      Status(fields["status"]),
		RequestHash: fields["request_hash"],
		Headers:     decodeHeaders(fields["resp_headers"]),
		BodyRef:     fields["body_ref"],
		TTLMs:       ttl.Milliseconds(),
	}

	if v := fields["http_status"]; v != "" {
		rec.HTTPStatus, _ = strconv.Atoi(v)
	}
	if v := fields["created_at"]; v != "" {
		rec.CreatedAt, _ = strconv.ParseInt(v, 10, 64)
	}
	if v := fields["completed_at"]; v != "" {
		rec.CompletedAt, _ = strconv.ParseInt(v, 10, 64)
	}
	if v := fields["lock_until"]; v != "" {
		rec.LockUntil, _ = strconv.ParseInt(v, 10, 64)
	}

	var body []byte
	if fields["body_ref"] == "external" {
		body, err = s.rdb.Get(ctx, bodyKey(scope, key)).Bytes()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("%w: %w", ErrStoreUnavailable, err)
		}
	} else if b := fields["resp_body"]; b != "" {
		body = []byte(b)
	}

	rec.BodySize = len(body)
	if len(body) > 0 {
		preview := body
		if len(preview) > bodyPreviewMax {
			preview = preview[:bodyPreviewMax]
		}
		rec.BodyPreview = string(preview)
	}

	return rec, nil
}

// DeleteRecord removes an idempotency key (ops recovery for stuck processing keys).
func (s *RedisStore) DeleteRecord(ctx context.Context, scope, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	pipe := s.rdb.Pipeline()
	pipe.Del(ctx, metaKey(scope, key), bodyKey(scope, key))
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrStoreUnavailable, err)
	}
	return nil
}

// ScopeForTenantUser builds the scope hash used by the sidecar.
func ScopeForTenantUser(tenantID, userID string) string {
	return BuildScope(tenantID, userID)
}

// LockRemainingMs returns milliseconds until processing lock expires (0 if not processing).
func (r *AdminRecord) LockRemainingMs() int64 {
	if r.Status != StatusProcessing || r.LockUntil == 0 {
		return 0
	}
	rem := r.LockUntil - time.Now().UnixMilli()
	if rem < 0 {
		return 0
	}
	return rem
}
