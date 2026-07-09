// Runtime limit overrides stored in Redis with a generation-validated local cache.
//
// A monotonic Redis generation counter (config:generation) increments on every
// write/delete. Before serving a cached entry, replicas compare the local
// generation snapshot to Redis so admin changes become visible on the next read
// without waiting for TTL expiry and without Pub/Sub missed-message risk.
package override

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

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

func (s *Store) GetGlobalOverride() (Config, bool) {
	return s.getOverride("global", "default")
}

func (s *Store) GetUserOverride(userID string) (Config, bool) {
	return s.getOverride("user", userID)
}

func (s *Store) GetTenantOverride(tenantID string) (Config, bool) {
	return s.getOverride("tenant", tenantID)
}

func (s *Store) GetEndpointOverride(tenantID, endpoint string) (Config, bool) {
	return s.getOverride("endpoint", EndpointOverrideID(tenantID, endpoint))
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
	if err == redis.Nil {
		gen = 0
	} else if err != nil {
		return
	}
	if gen == s.localGeneration.Load() {
		return
	}
	s.cache.Range(func(k, _ any) bool {
		s.cache.Delete(k)
		return true
	})
	s.localGeneration.Store(gen)
}

func (s *Store) getOverride(level, id string) (Config, bool) {
	key := s.key(level, id)

	if val, ok := s.cache.Load(key); ok {
		entry := val.(*cachedEntry)
		if time.Now().Before(entry.expiry) {
			return entry.cfg, true
		}
		s.cache.Delete(key)
	}

	ctx := context.Background()
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

func (s *Store) SetOverride(level, id string, cfg Config) error {
	key := s.key(level, id)
	ctx := context.Background()
	pipe := s.rdb.Pipeline()
	pipe.HSet(ctx, key, "capacity", cfg.Capacity, "refill_rate", cfg.RefillRate)
	incr := pipe.Incr(ctx, generationKey)
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	gen, _ := incr.Result()
	s.localGeneration.Store(gen)
	s.cache.Delete(key)
	return nil
}

func (s *Store) DeleteOverride(level, id string) error {
	key := s.key(level, id)
	ctx := context.Background()
	pipe := s.rdb.Pipeline()
	pipe.Del(ctx, key)
	incr := pipe.Incr(ctx, generationKey)
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	gen, _ := incr.Result()
	s.localGeneration.Store(gen)
	s.cache.Delete(key)
	return nil
}
