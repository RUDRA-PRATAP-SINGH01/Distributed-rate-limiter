package main

type RateLimiter interface {
    Allow(userID string) bool
}