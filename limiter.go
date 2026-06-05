package main

type RateLimiter interface {
	Allow(userID string) (allowed bool, remaining int, err error)
}
