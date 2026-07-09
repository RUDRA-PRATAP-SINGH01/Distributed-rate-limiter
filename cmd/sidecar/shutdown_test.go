package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter/internal/identity"
)

// Test Suite 20: Graceful shutdown
func TestSidecar_GracefulShutdown(t *testing.T) {
	fixture, cleanup := newTestFixture(t, false, 100*time.Millisecond, false)
	defer cleanup()

	// Upstream blocks until blockChan is released
	blockChan := make(chan struct{})
	fixture.upstreamHandler.mu.Lock()
	fixture.upstreamHandler.blockChan = blockChan
	fixture.upstreamHandler.mu.Unlock()

	// We start standard httptest.NewServer
	// which has native Close/Shutdown mechanisms. Let's use httptest.NewServer for simplicity:
	// httptest.NewServer internally starts a listener and server.
	testSrv := httptest.NewServer(fixture.sidecar)
	defer testSrv.Close()

	var (
		wg             sync.WaitGroup
		clientStatus   int
		clientBody     string
		clientErr      error
		clientComplete = make(chan struct{})
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(clientComplete)

		req, err := http.NewRequest(http.MethodGet, testSrv.URL+"/api/data", nil)
		if err != nil {
			clientErr = err
			return
		}
		req.Header.Set(identity.UserIDHeader, "user-shutdown")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			clientErr = err
			return
		}
		defer resp.Body.Close()

		clientStatus = resp.StatusCode
		bodyBytes, _ := io.ReadAll(resp.Body)
		clientBody = string(bodyBytes)
	}()

	// Wait briefly for the client request to reach upstream and block
	time.Sleep(20 * time.Millisecond)

	// Initiate server shutdown in background
	shutdownDone := make(chan struct{})
	go func() {
		// Gracefully shutdown the underlying http.Server
		_ = testSrv.Config.Shutdown(context.Background())
		close(shutdownDone)
	}()

	// Verify that shutdown is waiting (not finished immediately)
	select {
	case <-shutdownDone:
		t.Fatal("server shutdown completed immediately while an in-flight request was active")
	case <-time.After(15 * time.Millisecond):
		// Expected: shutdown is waiting
	}

	// Release the block so the in-flight request can complete
	close(blockChan)

	// Wait for client and server shutdown to finish
	wg.Wait()
	<-shutdownDone

	if clientErr != nil {
		t.Fatalf("client request failed: %v", clientErr)
	}
	if clientStatus != http.StatusOK {
		t.Errorf("expected client status 200, got %d", clientStatus)
	}
	if clientBody != "upstream-ok" {
		t.Errorf("expected body 'upstream-ok', got %q", clientBody)
	}
}
