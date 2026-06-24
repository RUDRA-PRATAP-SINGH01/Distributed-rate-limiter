package redis

import (
	"testing"
)

func TestLoadConfigStandalone(t *testing.T) {
	t.Setenv("REDIS_MODE", "standalone")
	t.Setenv("REDIS_ADDR", "localhost:6380")
	cfg := LoadConfigFromEnv()
	if cfg.Mode != ModeStandalone || cfg.Addr != "localhost:6380" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
}

func TestLoadConfigSentinel(t *testing.T) {
	t.Setenv("REDIS_MODE", "sentinel")
	t.Setenv("REDIS_SENTINEL_ADDRS", "s1:26379,s2:26379")
	t.Setenv("REDIS_MASTER_NAME", "mymaster")
	cfg := LoadConfigFromEnv()
	if cfg.Mode != ModeSentinel || len(cfg.SentinelAddrs) != 2 || cfg.MasterName != "mymaster" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
}

func TestNewStandaloneClient(t *testing.T) {
	cfg := DefaultConfig()
	client := New(cfg)
	if client == nil {
		t.Fatal("expected client")
	}
	_ = client.Close()
}
