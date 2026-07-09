package limiter

// test_helpers_test.go — shared miniredis setup, Redis state inspection helpers,
// and real-Redis opt-in plumbing for internal/limiter tests.

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

var (
	sharedMR    *miniredis.Miniredis
	sharedRdb   redis.UniversalClient
	isRealRedis bool
)

func TestMain(m *testing.M) {
	redisAddr := os.Getenv("REDIS_TEST_ADDR")
	if redisAddr != "" {
		isRealRedis = true
		password := os.Getenv("REDIS_TEST_PASSWORD")
		if password == "" {
			password = "dev-redis-password" // default developer compose password
		}
		sharedRdb = redis.NewClient(&redis.Options{
			Addr:     redisAddr,
			Password: password,
			PoolSize: 1000, // Large pool size to accommodate concurrency tests
		})
	} else {
		mr, err := miniredis.Run()
		if err != nil {
			fmt.Printf("failed to start miniredis: %v\n", err)
			os.Exit(1)
		}
		sharedMR = mr
		sharedRdb = redis.NewClient(&redis.Options{
			Addr:     mr.Addr(),
			PoolSize: 1000,
		})
	}

	code := m.Run()

	_ = sharedRdb.Close()
	if sharedMR != nil {
		sharedMR.Close()
	}
	os.Exit(code)
}

// newMR returns the shared miniredis instance and resets the database.
func newMR(t *testing.T) (*miniredis.Miniredis, redis.UniversalClient) {
	t.Helper()
	ctx := context.Background()
	// Flush DB to ensure test isolation
	if err := sharedRdb.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("FlushDB failed: %v", err)
	}
	return sharedMR, sharedRdb
}

// readTokenBucketState reads 'tokens' and 'last_refill' from Redis for a rate: key.
// Returns raw float string for tokens so callers can verify fractional precision.
func readTokenBucketState(t *testing.T, rdb redis.UniversalClient, userID string) (tokensStr string, lastRefillStr string) {
	t.Helper()
	ctx := context.Background()
	key := fmt.Sprintf("rate:%s", userID)
	vals, err := rdb.HMGet(ctx, key, "tokens", "last_refill").Result()
	if err != nil {
		t.Fatalf("HMGet(%s): %v", key, err)
	}
	if vals[0] == nil {
		return "", ""
	}
	return fmt.Sprint(vals[0]), fmt.Sprint(vals[1])
}

// readTokensFloat reads the stored float token count directly from Redis.
func readTokensFloat(t *testing.T, rdb redis.UniversalClient, userID string) float64 {
	t.Helper()
	raw, _ := readTokenBucketState(t, rdb, userID)
	if raw == "" {
		return -1
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		t.Fatalf("ParseFloat(%q): %v", raw, err)
	}
	return f
}

// keyExists returns whether a Redis key exists.
func keyExists(t *testing.T, rdb redis.UniversalClient, key string) bool {
	t.Helper()
	ctx := context.Background()
	n, err := rdb.Exists(ctx, key).Result()
	if err != nil {
		t.Fatalf("EXISTS(%s): %v", key, err)
	}
	return n > 0
}

// readTTL returns the TTL in seconds of a Redis key (-1 = no TTL, -2 = missing).
func readTTL(t *testing.T, rdb redis.UniversalClient, key string) int64 {
	t.Helper()
	ctx := context.Background()
	dur, err := rdb.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("TTL(%s): %v", key, err)
	}
	return int64(dur.Seconds())
}

// zCard returns the cardinality of a ZSET.
func zCard(t *testing.T, rdb redis.UniversalClient, key string) int64 {
	t.Helper()
	ctx := context.Background()
	n, err := rdb.ZCard(ctx, key).Result()
	if err != nil {
		t.Fatalf("ZCARD(%s): %v", key, err)
	}
	return n
}

// readHierarchyTokens reads the persisted tokens for all 4 hierarchy keys
// as floats. keys must be [global, tenant, user, endpoint].
func readHierarchyTokens(t *testing.T, rdb redis.UniversalClient, keys []string) [4]float64 {
	t.Helper()
	var out [4]float64
	for i, k := range keys {
		ctx := context.Background()
		raw, err := rdb.HGet(ctx, k, "tokens").Result()
		if err == redis.Nil {
			out[i] = -1
			continue
		}
		if err != nil {
			t.Fatalf("HGet(%s tokens): %v", k, err)
		}
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			t.Fatalf("ParseFloat(%q): %v", raw, err)
		}
		out[i] = f
	}
	return out
}

// hierarchyKeys produces unique test-scoped keys to avoid cross-test interference.
func hierarchyKeys(prefix string) []string {
	return []string{
		"hier:" + prefix + ":global",
		"hier:" + prefix + ":tenant",
		"hier:" + prefix + ":user",
		"hier:" + prefix + ":endpoint",
	}
}
