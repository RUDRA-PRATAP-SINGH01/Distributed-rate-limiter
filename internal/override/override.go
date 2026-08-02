// Package override holds runtime limit overrides stored in Redis with a
// generation-validated local cache.
//
// A monotonic Redis generation counter (config:generation) increments on every
// write/delete. Before serving a cached entry, replicas compare the local
// generation snapshot to Redis so admin changes become visible on the next read
// without waiting for TTL expiry and without Pub/Sub missed-message risk.
package override

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/metrics"
	"github.com/redis/go-redis/v9"
)

const generationKey = "config:generation"

type Config struct {
	Capacity   int     `json:"capacity"`
	RefillRate float64 `json:"refill_rate,omitempty"`
}

type Store struct {
	rdb   redis.UniversalClient
	cache sync.Map // keyed by full Redis key; safe for concurrent readers
	ttl   time.Duration

	localGeneration atomic.Int64
}

type cachedEntry struct {
	cfg    Config
	expiry time.Time
}

func NewStore(rdb redis.UniversalClient, ttl time.Duration) *Store {
	return &Store{rdb: rdb, ttl: ttl}
}

func (s *Store) key(level, id string) string {
	return fmt.Sprintf("config:%s:%s", level, id)
}

func (s *Store) GetGlobalOverride(ctx context.Context) (Config, bool) {
	return s.getOverride(ctx, "global", "default")
}

func (s *Store) GetUserOverride(ctx context.Context, userID string) (Config, bool) {
	return s.getOverride(ctx, "user", userID)
}

func (s *Store) GetTenantOverride(ctx context.Context, tenantID string) (Config, bool) {
	return s.getOverride(ctx, "tenant", tenantID)
}

func (s *Store) GetEndpointOverride(ctx context.Context, tenantID, endpoint string) (Config, bool) {
	return s.getOverride(ctx, "endpoint", EndpointOverrideID(tenantID, endpoint))
}

// EndpointOverrideID scopes endpoint limits per tenant — two tenants can share a path
// like /api/login without sharing the same bucket.
func EndpointOverrideID(tenantID, endpoint string) string {
	return tenantID + "|" + endpoint
}

// RefreshGeneration clears the local cache when Redis reports a newer generation.
// Call once per hierarchical check before reading multiple override levels.
func (s *Store) RefreshGeneration(ctx context.Context) {
	gen, err := s.rdb.Get(ctx, generationKey).Int64()
	if errors.Is(err, redis.Nil) {
		gen = 0
	} else if err != nil {
		metrics.RecordOverrideGenerationRefreshError()
		return
	}
	metrics.RecordOverrideGeneration(float64(gen))
	if gen == s.localGeneration.Load() {
		return
	}
	s.cache.Range(func(k, _ any) bool {
		s.cache.Delete(k)
		return true
	})
	s.localGeneration.Store(gen)
	metrics.RecordOverrideCacheInvalidation()
}

// getOverride loads an override level from cache or Redis.
// On cache miss with a cancelled context or Redis timeout/error, it returns
// (Config{}, false) so the caller safely falls back to static defaults (M-02).
func (s *Store) getOverride(ctx context.Context, level, id string) (Config, bool) {
	key := s.key(level, id)

	if val, ok := s.cache.Load(key); ok {
		entry := val.(*cachedEntry)
		if time.Now().Before(entry.expiry) {
			return entry.cfg, true
		}
		s.cache.Delete(key)
	}

	data, err := s.rdb.HGetAll(ctx, key).Result()
	if err != nil || len(data) == 0 {
		return Config{}, false
	}

	capacity, _ := strconv.Atoi(data["capacity"])
	rate, _ := strconv.ParseFloat(data["refill_rate"], 64)
	cfg := Config{Capacity: capacity, RefillRate: rate}
	s.cache.Store(key, &cachedEntry{cfg: cfg, expiry: time.Now().Add(s.ttl)})
	return cfg, true
}

func (s *Store) SetOverride(ctx context.Context, level, id string, cfg Config) error {
	key := s.key(level, id)
	pipe := s.rdb.Pipeline()
	pipe.HSet(ctx, key, "capacity", cfg.Capacity, "refill_rate", cfg.RefillRate)
	incr := pipe.Incr(ctx, generationKey)
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	gen, _ := incr.Result()
	s.localGeneration.Store(gen)
	s.cache.Delete(key)
	metrics.RecordOverrideGeneration(float64(gen))
	return nil
}

func (s *Store) DeleteOverride(ctx context.Context, level, id string) error {
	key := s.key(level, id)
	pipe := s.rdb.Pipeline()
	pipe.Del(ctx, key)
	incr := pipe.Incr(ctx, generationKey)
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	gen, _ := incr.Result()
	s.localGeneration.Store(gen)
	s.cache.Delete(key)
	metrics.RecordOverrideGeneration(float64(gen))
	return nil
}
