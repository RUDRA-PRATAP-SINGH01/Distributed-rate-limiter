package main

import (
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

	// Hierarchical limits
	GlobalCapacity       int
	GlobalRefillRate     float64
	TenantCapacity       int
	TenantRefillRate     float64
	UserCapacity         int
	UserRefillRate       float64
	EndpointCapacity     int
	EndpointRefillRate   float64
}

func LoadConfig() Config {
	port, _ := strconv.Atoi(getEnv("PORT", "8080"))
	capacity, _ := strconv.Atoi(getEnv("CAPACITY", "10"))
	refillRate, _ := strconv.ParseFloat(getEnv("REFILL_RATE", "1.0"), 64)
	windowSec, _ := strconv.Atoi(getEnv("WINDOW_SEC", "60"))

	// Hierarchical defaults (sane initial values)
	globalCap, _ := strconv.Atoi(getEnv("GLOBAL_CAPACITY", "1000000"))
	globalRate, _ := strconv.ParseFloat(getEnv("GLOBAL_REFILL_RATE", "10000.0"), 64)
	tenantCap, _ := strconv.Atoi(getEnv("TENANT_CAPACITY", "100000"))
	tenantRate, _ := strconv.ParseFloat(getEnv("TENANT_REFILL_RATE", "1000.0"), 64)
	userCap, _ := strconv.Atoi(getEnv("USER_CAPACITY", "100"))
	userRate, _ := strconv.ParseFloat(getEnv("USER_REFILL_RATE", "1.0"), 64)
	endpointCap, _ := strconv.Atoi(getEnv("ENDPOINT_CAPACITY", "10"))
	endpointRate, _ := strconv.ParseFloat(getEnv("ENDPOINT_REFILL_RATE", "0.5"), 64)

	return Config{
		Port:       port,
		RedisAddr:  getEnv("REDIS_ADDR", "localhost:6379"),
		Algorithm:  getEnv("ALGORITHM", "token"),
		Capacity:   capacity,
		RefillRate: refillRate,
		WindowSec:  windowSec,

		GlobalCapacity:       globalCap,
		GlobalRefillRate:     globalRate,
		TenantCapacity:       tenantCap,
		TenantRefillRate:     tenantRate,
		UserCapacity:         userCap,
		UserRefillRate:       userRate,
		EndpointCapacity:     endpointCap,
		EndpointRefillRate:   endpointRate,
	}
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
