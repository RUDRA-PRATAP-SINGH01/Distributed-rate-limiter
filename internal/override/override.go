package override

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type Config struct {
	Capacity   int     `json:"capacity"`
	RefillRate float64 `json:"refill_rate,omitempty"`
}

type Store struct {
	rdb   *redis.Client
	cache sync.Map
	ttl   time.Duration
}

type cachedEntry struct {
	cfg    Config
	expiry time.Time
}

func NewStore(rdb *redis.Client, ttl time.Duration) *Store {
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

func (s *Store) GetEndpointOverride(endpoint string) (Config, bool) {
	return s.getOverride("endpoint", endpoint)
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

	data, err := s.rdb.HGetAll(context.Background(), key).Result()
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
	if err := s.rdb.HSet(ctx, key, "capacity", cfg.Capacity, "refill_rate", cfg.RefillRate).Err(); err != nil {
		return err
	}
	s.cache.Delete(key)
	return nil
}

func (s *Store) DeleteOverride(level, id string) error {
	key := s.key(level, id)
	ctx := context.Background()
	if err := s.rdb.Del(ctx, key).Err(); err != nil {
		return err
	}
	s.cache.Delete(key)
	return nil
}
