package limiter

import "math"

const (
	minBucketTTLSec = 1
	maxBucketTTLSec = 86400 // 24h safety ceiling
)

// bucketTTLSeconds is the idle lifetime for a token-bucket Redis key:
// ceil(capacity / refillRate), clamped to [1, 86400].
// Mirrors bucket_ttl_sec() in token_bucket.lua / hierarchical.lua (L-02).
func bucketTTLSeconds(capacity int, refillRate float64) int {
	if capacity <= 0 || refillRate <= 0 {
		return minBucketTTLSec
	}
	ttl := int(math.Ceil(float64(capacity) / refillRate))
	if ttl < minBucketTTLSec {
		return minBucketTTLSec
	}
	if ttl > maxBucketTTLSec {
		return maxBucketTTLSec
	}
	return ttl
}
