package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	redisclient "github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/redis"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestSidecarHealth_LimiterAndRedisHealthy(t *testing.T) {
	mr := startMiniRedis(t)
	defer mr.Close()

	limiterSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	}))
	defer limiterSrv.Close()

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	handler := newSidecarHealthHandler(sidecarHealthDeps{
		needsRedis:  true,
		limiterURL:  limiterSrv.URL,
		httpClient:  http.DefaultClient,
		redisClient: rdb,
		redisCfg:    redisclient.Config{Mode: redisclient.ModeStandalone},
	})

	code, body := doHealthRequest(t, handler)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%v", code, body)
	}
	if body["status"] != "healthy" {
		t.Fatalf("expected healthy status, got %v", body["status"])
	}
	redisBlock, ok := body["redis"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected redis block, got %T", body["redis"])
	}
	if redisBlock["connected"] != true {
		t.Fatalf("expected connected redis, got %v", redisBlock["connected"])
	}
}

func TestSidecarHealth_LimiterUnhealthyRedisHealthy(t *testing.T) {
	mr := startMiniRedis(t)
	defer mr.Close()

	limiterSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"unhealthy"}`))
	}))
	defer limiterSrv.Close()

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	handler := newSidecarHealthHandler(sidecarHealthDeps{
		needsRedis:  true,
		limiterURL:  limiterSrv.URL,
		httpClient:  http.DefaultClient,
		redisClient: rdb,
		redisCfg:    redisclient.Config{Mode: redisclient.ModeStandalone},
	})

	code, body := doHealthRequest(t, handler)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", code)
	}
	if body["status"] != "unhealthy" {
		t.Fatalf("expected unhealthy, got %v", body["status"])
	}
	if _, hasRedis := body["redis"]; hasRedis {
		t.Fatal("redis block should not be returned when limiter is unhealthy")
	}
}

func TestSidecarHealth_LimiterHealthyRedisUnhealthy(t *testing.T) {
	mr := startMiniRedis(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	mr.Close() // isolate sidecar Redis failure while limiter stays healthy

	limiterSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy","redis":{"connected":true}}`))
	}))
	defer limiterSrv.Close()

	handler := newSidecarHealthHandler(sidecarHealthDeps{
		needsRedis:  true,
		limiterURL:  limiterSrv.URL,
		httpClient:  http.DefaultClient,
		redisClient: rdb,
		redisCfg:    redisclient.Config{Mode: redisclient.ModeStandalone},
	})

	code, body := doHealthRequest(t, handler)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%v", code, body)
	}
	if body["status"] != "unhealthy" {
		t.Fatalf("expected unhealthy, got %v", body["status"])
	}
	redisBlock, ok := body["redis"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected redis block, got %T", body["redis"])
	}
	if redisBlock["connected"] != false {
		t.Fatalf("expected disconnected redis, got %v", redisBlock["connected"])
	}
}

func TestSidecarHealth_RedisNotRequiredLimiterHealthy(t *testing.T) {
	limiterSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	}))
	defer limiterSrv.Close()

	handler := newSidecarHealthHandler(sidecarHealthDeps{
		needsRedis: false,
		limiterURL: limiterSrv.URL,
		httpClient: http.DefaultClient,
	})

	code, body := doHealthRequest(t, handler)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if body["status"] != "healthy" {
		t.Fatalf("expected healthy, got %v", body["status"])
	}
	if _, hasRedis := body["redis"]; hasRedis {
		t.Fatal("redis block should not be present when needsRedis is false")
	}
}

func startMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	return mr
}

func doHealthRequest(t *testing.T, handler http.HandlerFunc) (int, map[string]interface{}) {
	t.Helper()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("health request: %v", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return resp.StatusCode, body
}
