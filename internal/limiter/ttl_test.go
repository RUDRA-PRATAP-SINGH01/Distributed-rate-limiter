package limiter

import (
	"context"
	"math"
	"testing"
)

func TestBucketTTLSeconds(t *testing.T) {
	cases := []struct {
		cap  int
		rate float64
		want int
	}{
		{10, 1.0, 10},
		{10, 0.5, 20},
		{1, 1.0, 1},
		{100, 10.0, 10},
		{10, 0.0001, maxBucketTTLSec}, // ceil(100000) clamped
		{0, 1.0, minBucketTTLSec},
		{10, 0, minBucketTTLSec},
		{10, -1, minBucketTTLSec},
	}
	for _, tc := range cases {
		got := bucketTTLSeconds(tc.cap, tc.rate)
		if got != tc.want {
			t.Fatalf("bucketTTLSeconds(%d, %v)=%d want %d", tc.cap, tc.rate, got, tc.want)
		}
	}
}

func TestTokenBucket_TTLMatchesRefillHorizon(t *testing.T) {
	cases := []struct {
		cap  int
		rate float64
	}{
		{10, 1.0},
		{5, 0.5},
		{100, 10.0},
		{10, 0.0001}, // must clamp to 24h, not 3600 or 100000
	}
	for _, tc := range cases {
		tc := tc
		t.Run("", func(t *testing.T) {
			tb, rdb, _ := newTB(t, tc.cap, tc.rate)
			uid := "ttl-user"
			if _, _, err := tb.Allow(context.Background(), uid); err != nil {
				t.Fatal(err)
			}
			want := bucketTTLSeconds(tc.cap, tc.rate)
			ttl := readTTL(t, rdb, "rate:"+uid)
			// miniredis TTL is remaining seconds; allow ±1s skew.
			if math.Abs(float64(ttl)-float64(want)) > 1 {
				t.Fatalf("TTL=%d want ~%d (cap=%d rate=%v); must not be hardcoded 3600", ttl, want, tc.cap, tc.rate)
			}
			if want != 3600 && ttl == 3600 {
				t.Fatal("TTL still hardcoded to 3600")
			}
		})
	}
}

func TestHierarchical_TTLPerLevel(t *testing.T) {
	mr, rdb := newMR(t)
	// Distinct horizons per level so TTLs must differ.
	hl := NewHierarchicalLimiter(
		rdb,
		1000, 100, 10, 5,
		10.0, 10.0, 1.0, 0.5, // TTLs: 100, 10, 10, 10
	)
	keys := []string{"rate:global", "rate:tenant:t", "rate:user:u", "rate:endpoint:t:e"}
	caps := []int{1000, 100, 10, 5}
	rates := []float64{10.0, 10.0, 1.0, 0.5}

	if _, _, err := hl.AllowWithParams(context.Background(), keys, caps, rates); err != nil {
		t.Fatal(err)
	}

	for i, key := range keys {
		want := bucketTTLSeconds(caps[i], rates[i])
		ttl := readTTL(t, rdb, key)
		if math.Abs(float64(ttl)-float64(want)) > 1 {
			t.Errorf("level %d key %s TTL=%d want ~%d", i, key, ttl, want)
		}
	}
	_ = mr
}
