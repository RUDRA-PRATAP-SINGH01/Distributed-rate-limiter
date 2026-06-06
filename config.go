package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
)

type Config struct {
	Port       int
	RedisAddr  string
	Algorithm  string
	Capacity   int
	RefillRate float64
	WindowSec  int

	EnableHierarchical bool
	EnableAdminAPI     bool
	AdminPort          int
	AdminAPIKey        string
	OverrideCacheTTLMs int

	// Hierarchical limits
	GlobalCapacity     int
	GlobalRefillRate   float64
	TenantCapacity     int
	TenantRefillRate   float64
	UserCapacity       int
	UserRefillRate     float64
	EndpointCapacity   int
	EndpointRefillRate float64
}

func (c Config) AdminAddr() string {
	return fmt.Sprintf(":%d", c.AdminPort)
}

func LoadConfig() Config {
	return Config{
		Port:               parseIntEnv("PORT", "8080"),
		RedisAddr:          getEnv("REDIS_ADDR", "localhost:6379"),
		Algorithm:          getEnv("ALGORITHM", "token"),
		Capacity:           parseIntEnv("CAPACITY", "10"),
		RefillRate:         parseFloatEnv("REFILL_RATE", "1.0"),
		WindowSec:          parseIntEnv("WINDOW_SEC", "60"),
		EnableHierarchical: getEnv("ENABLE_HIERARCHICAL", "true") == "true",
		EnableAdminAPI:     getEnv("ENABLE_ADMIN_API", "true") == "true",
		AdminPort:          parseIntEnv("ADMIN_PORT", "8082"),
		AdminAPIKey:        getEnv("ADMIN_API_KEY", "dev-key-change-in-prod"),
		OverrideCacheTTLMs: parseIntEnv("OVERRIDE_CACHE_TTL_MS", "5000"),

		GlobalCapacity:     parseIntEnv("GLOBAL_CAPACITY", "1000000"),
		GlobalRefillRate:   parseFloatEnv("GLOBAL_REFILL_RATE", "10000.0"),
		TenantCapacity:     parseIntEnv("TENANT_CAPACITY", "100000"),
		TenantRefillRate:   parseFloatEnv("TENANT_REFILL_RATE", "1000.0"),
		UserCapacity:       parseIntEnv("USER_CAPACITY", "100"),
		UserRefillRate:     parseFloatEnv("USER_REFILL_RATE", "1.0"),
		EndpointCapacity:   parseIntEnv("ENDPOINT_CAPACITY", "10"),
		EndpointRefillRate: parseFloatEnv("ENDPOINT_REFILL_RATE", "0.5"),
	}
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func parseIntEnv(key, defaultVal string) int {
	raw := getEnv(key, defaultVal)
	value, err := strconv.Atoi(raw)
	defaultInt, _ := strconv.Atoi(defaultVal)
	if err != nil || value <= 0 {
		if raw != defaultVal {
			log.Printf("WARNING: invalid %s=%q, using default %s", key, raw, defaultVal)
		}
		return defaultInt
	}
	return value
}

func parseFloatEnv(key, defaultVal string) float64 {
	raw := getEnv(key, defaultVal)
	value, err := strconv.ParseFloat(raw, 64)
	defaultFloat, _ := strconv.ParseFloat(defaultVal, 64)
	if err != nil || value <= 0 {
		if raw != defaultVal {
			log.Printf("WARNING: invalid %s=%q, using default %s", key, raw, defaultVal)
		}
		return defaultFloat
	}
	return value
}
