package main

import (
	"bytes"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
)

func TestConcurrency_RateChecking(t *testing.T) {
	const capacity = 50
	const totalRequests = 150

	fixture, cleanup := newTestFixture(t, func(c *Config) {
		c.Algorithm = "token"
		c.Capacity = capacity
		c.RefillRate = 0.0001 // practically zero refill during short test
	})
	defer cleanup()

	var (
		allowed     atomic.Int64
		denied      atomic.Int64
		clientError atomic.Int64
		serverError atomic.Int64
		unexpected  atomic.Int64
		wg          sync.WaitGroup
	)

	barrier := make(chan struct{})

	for i := 0; i < totalRequests; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-barrier

			req, err := http.NewRequest(http.MethodGet, fixture.mainURL+"/check", nil)
			if err != nil {
				clientError.Add(1)
				return
			}
			req.Header.Set("X-API-Key", fixture.cfg.InternalAPIKey)
			req.Header.Set("X-User-ID", "concur-user")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				serverError.Add(1)
				return
			}
			defer resp.Body.Close()

			switch resp.StatusCode {
			case http.StatusOK:
				allowed.Add(1)
			case http.StatusTooManyRequests:
				denied.Add(1)
			case http.StatusBadRequest:
				clientError.Add(1)
			case http.StatusServiceUnavailable, http.StatusInternalServerError:
				serverError.Add(1)
			default:
				unexpected.Add(1)
			}
		}(i)
	}

	// Release all goroutines concurrently
	close(barrier)
	wg.Wait()

	gotAllowed := allowed.Load()
	gotDenied := denied.Load()
	gotClient := clientError.Load()
	gotServer := serverError.Load()
	gotUnexp := unexpected.Load()

	totalRecorded := gotAllowed + gotDenied + gotClient + gotServer + gotUnexp
	if totalRecorded != totalRequests {
		t.Errorf("expected sum of status codes to be %d, got %d", totalRequests, totalRecorded)
	}

	if gotAllowed != capacity {
		t.Errorf("expected exactly %d allowed requests, got %d", capacity, gotAllowed)
	}
	if gotDenied != totalRequests-capacity {
		t.Errorf("expected exactly %d denied requests, got %d", totalRequests-capacity, gotDenied)
	}
	if gotClient != 0 || gotServer != 0 || gotUnexp != 0 {
		t.Errorf("expected 0 errors, got clientErr=%d, serverErr=%d, unexpected=%d", gotClient, gotServer, gotUnexp)
	}
}

func TestConcurrency_AdminOverrides(t *testing.T) {
	const totalMutations = 100
	fixture, cleanup := newTestFixture(t, nil)
	defer cleanup()

	var (
		success    atomic.Int64
		conflict   atomic.Int64
		clientErr  atomic.Int64
		serverErr  atomic.Int64
		unexpected atomic.Int64
		wg         sync.WaitGroup
	)

	barrier := make(chan struct{})

	for i := 0; i < totalMutations; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-barrier

			body := fmt.Sprintf(`{"capacity": %d}`, 10+id)
			req, err := http.NewRequest(http.MethodPost, fixture.adminURL+"/admin/limits/user/alice", bytes.NewBufferString(body))
			if err != nil {
				clientErr.Add(1)
				return
			}
			req.Header.Set("X-API-Key", fixture.cfg.AdminAPIKey)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				serverErr.Add(1)
				return
			}
			defer resp.Body.Close()

			switch resp.StatusCode {
			case http.StatusNoContent:
				success.Add(1)
			case http.StatusConflict:
				conflict.Add(1)
			case http.StatusBadRequest:
				clientErr.Add(1)
			case http.StatusUnauthorized:
				serverErr.Add(1)
			default:
				unexpected.Add(1)
			}
		}(i)
	}

	close(barrier)
	wg.Wait()

	gotSuccess := success.Load()
	gotConflict := conflict.Load()
	gotClient := clientErr.Load()
	gotServer := serverErr.Load()
	gotUnexp := unexpected.Load()

	totalRecorded := gotSuccess + gotConflict + gotClient + gotServer + gotUnexp
	if totalRecorded != totalMutations {
		t.Errorf("expected sum of mutation outcomes to be %d, got %d", totalMutations, totalRecorded)
	}

	if gotSuccess != totalMutations {
		t.Errorf("expected %d successful updates, got %d (clientErr=%d, serverErr=%d, unexpected=%d)", totalMutations, gotSuccess, gotClient, gotServer, gotUnexp)
	}
}
