package routing

import (
	"os"
	"strconv"
	"strings"
)

// Config tunes scoring, circuit breaking, and health probes.
type Config struct {
	TargetLatencyMs   float64
	EMAAlpha          float64
	ErrorPenalty      float64
	MinHealthScore    float64
	CircuitErrorRate  float64
	CircuitMinSamples int64
	CircuitCooldownMs int64
	MaxFailoverTries  int
	ProbeIntervalSec  int
}

func DefaultConfig() Config {
	return Config{
		TargetLatencyMs:   100,
		EMAAlpha:          0.2,
		ErrorPenalty:      2.0,
		MinHealthScore:    20,
		CircuitErrorRate:  0.5,
		CircuitMinSamples: 10,
		CircuitCooldownMs: 30_000,
		MaxFailoverTries:  3,
		ProbeIntervalSec:  15,
	}
}

func LoadConfigFromEnv() Config {
	cfg := DefaultConfig()
	if v := parseFloatEnv("ROUTING_TARGET_LATENCY_MS", cfg.TargetLatencyMs); v > 0 {
		cfg.TargetLatencyMs = v
	}
	if v := parseFloatEnv("ROUTING_EMA_ALPHA", cfg.EMAAlpha); v > 0 && v <= 1 {
		cfg.EMAAlpha = v
	}
	if v := parseFloatEnv("ROUTING_MIN_HEALTH_SCORE", cfg.MinHealthScore); v >= 0 {
		cfg.MinHealthScore = v
	}
	if v := parseFloatEnv("ROUTING_CIRCUIT_ERROR_RATE", cfg.CircuitErrorRate); v > 0 {
		cfg.CircuitErrorRate = v
	}
	if v := parseIntEnv("ROUTING_CIRCUIT_MIN_SAMPLES", int(cfg.CircuitMinSamples)); v > 0 {
		cfg.CircuitMinSamples = int64(v)
	}
	if v := parseIntEnv("ROUTING_CIRCUIT_COOLDOWN_MS", int(cfg.CircuitCooldownMs)); v > 0 {
		cfg.CircuitCooldownMs = int64(v)
	}
	if v := parseIntEnv("ROUTING_MAX_FAILOVER_TRIES", cfg.MaxFailoverTries); v > 0 {
		cfg.MaxFailoverTries = v
	}
	if v := parseIntEnv("ROUTING_PROBE_INTERVAL_SEC", cfg.ProbeIntervalSec); v > 0 {
		cfg.ProbeIntervalSec = v
	}
	return cfg
}

// ParseGatewaysEnv parses GATEWAYS=id|url|weight,id|url|weight
func ParseGatewaysEnv(raw string) ([]Gateway, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []Gateway
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.Split(part, "|")
		if len(fields) != 3 {
			continue
		}
		weight, _ := strconv.Atoi(strings.TrimSpace(fields[2]))
		if weight <= 0 {
			weight = 100
		}
		out = append(out, Gateway{
			ID:     strings.TrimSpace(fields[0]),
			URL:    strings.TrimSpace(fields[1]),
			Weight: weight,
		})
	}
	return out, nil
}

func GatewaysFromEnv() []Gateway {
	gws, err := ParseGatewaysEnv(os.Getenv("GATEWAYS"))
	if err != nil || len(gws) == 0 {
		return nil
	}
	return gws
}

func parseFloatEnv(key string, def float64) float64 {
	if raw := os.Getenv(key); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil {
			return v
		}
	}
	return def
}

func parseIntEnv(key string, def int) int {
	if raw := os.Getenv(key); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			return v
		}
	}
	return def
}
