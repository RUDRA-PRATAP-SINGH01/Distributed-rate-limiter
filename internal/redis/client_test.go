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

func TestLoadConfigCluster(t *testing.T) {
	t.Setenv("REDIS_MODE", "cluster")
	t.Setenv("REDIS_CLUSTER_ADDRS", "c1:6379,c2:6379,c3:6379")
	cfg := LoadConfigFromEnv()
	if cfg.Mode != ModeCluster || len(cfg.ClusterAddrs) != 3 {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
}

func TestConfigValidate(t *testing.T) {
	valid := []Config{
		{Mode: ModeStandalone},
		{Mode: ModeSentinel},
		{Mode: ModeCluster},
		{Mode: ""},
	}
	for _, c := range valid {
		if err := c.Validate(); err != nil {
			t.Errorf("expected valid config for mode %q, got error: %v", c.Mode, err)
		}
	}

	invalid := []Config{
		{Mode: "clusterr"},
		{Mode: "sentinell"},
		{Mode: "unknown"},
		{Mode: "redis"},
	}
	for _, c := range invalid {
		if err := c.Validate(); err == nil {
			t.Errorf("expected error for invalid mode %q, got nil", c.Mode)
		}
	}
}

func TestDescribeFormats(t *testing.T) {
	standaloneCfg := Config{Mode: ModeStandalone, Addr: "127.0.0.1:6379"}
	if got := Describe(standaloneCfg); got != "standalone addr=127.0.0.1:6379" {
		t.Errorf("Describe(standalone) = %q", got)
	}

	sentinelCfg := Config{Mode: ModeSentinel, MasterName: "master1", SentinelAddrs: []string{"s1:26379", "s2:26379"}}
	if got := Describe(sentinelCfg); got != "sentinel master=master1 sentinels=[s1:26379,s2:26379]" {
		t.Errorf("Describe(sentinel) = %q", got)
	}

	clusterCfg := Config{Mode: ModeCluster, ClusterAddrs: []string{"c1:6379", "c2:6379"}}
	if got := Describe(clusterCfg); got != "cluster addrs=[c1:6379,c2:6379]" {
		t.Errorf("Describe(cluster) = %q", got)
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

func TestNewUnknownModePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic for unknown mode, got nil")
		}
	}()
	cfg := Config{Mode: "bogus_mode"}
	_ = New(cfg)
}

func TestNewEmptyModeIsStandalone(t *testing.T) {
	client := New(Config{Mode: "", Addr: "127.0.0.1:1"})
	if client == nil {
		t.Fatal("empty mode must default to standalone, not panic")
	}
	_ = client.Close()
}
