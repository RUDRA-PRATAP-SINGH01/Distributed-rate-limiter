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
}

func LoadConfig() Config {
	port, _ := strconv.Atoi(getEnv("PORT", "8080"))
	capacity, _ := strconv.Atoi(getEnv("CAPACITY", "10"))
	refillRate, _ := strconv.ParseFloat(getEnv("REFILL_RATE", "1.0"), 64)
	windowSec, _ := strconv.Atoi(getEnv("WINDOW_SEC", "60"))

	return Config{
		Port:       port,
		RedisAddr:  getEnv("REDIS_ADDR", "localhost:6379"),
		Algorithm:  getEnv("ALGORITHM", "token"),
		Capacity:   capacity,
		RefillRate: refillRate,
		WindowSec:  windowSec,
	}
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
