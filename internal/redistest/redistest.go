// Package redistest gives test packages one way to reach Redis: a real server
// when REDIS_TEST_ADDR is set, miniredis otherwise.
//
// The two are not interchangeable. miniredis runs Lua on gopher-lua, keeps its
// own clock, and returns different error surfaces, so a script that passes
// in-memory can still fail against Redis. CI therefore runs these suites both
// ways rather than trusting the double.
//
// Against a real server the suites share one database, so callers must run
// packages serially (go test -p 1). Start flushes the database so each harness
// begins clean.
package redistest

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// AddrEnv names the variable that switches the suite to a real server.
const AddrEnv = "REDIS_TEST_ADDR"

// PasswordEnv is optional and only read when AddrEnv is set.
const PasswordEnv = "REDIS_TEST_PASSWORD"

// Server is a Redis instance for one test, backed either by a real server or
// by miniredis.
type Server struct {
	addr     string
	password string
	mini     *miniredis.Miniredis // nil when backed by a real server
}

// Start returns a Redis instance and registers its cleanup with t.
func Start(t testing.TB) *Server {
	t.Helper()

	if addr := os.Getenv(AddrEnv); addr != "" {
		s := &Server{addr: addr, password: os.Getenv(PasswordEnv)}
		client := s.Client(t)
		if err := client.FlushDB(context.Background()).Err(); err != nil {
			t.Fatalf("redistest: flush %s at %s: %v", AddrEnv, addr, err)
		}
		return s
	}

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("redistest: start miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	return &Server{addr: mr.Addr(), mini: mr}
}

// Addr is the host:port callers should dial.
func (s *Server) Addr() string { return s.addr }

// IsReal reports whether this is a real Redis server rather than miniredis.
func (s *Server) IsReal() bool { return s.mini == nil }

// Client opens a connection and closes it when the test ends.
func (s *Server) Client(t testing.TB) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{
		Addr:     s.addr,
		Password: s.password,
	})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

// FastForward advances expiry by d. miniredis jumps its clock; a real server
// has to be waited out, so keep the durations small.
func (s *Server) FastForward(d time.Duration) {
	if s.mini != nil {
		s.mini.FastForward(d)
		return
	}
	time.Sleep(d)
}

// SkipIfReal skips tests that need powers only the double has, such as killing
// the server mid-test or injecting a command error. Those still run in the
// miniredis pass, so the behavior stays covered.
func (s *Server) SkipIfReal(t testing.TB, reason string) {
	t.Helper()
	if s.IsReal() {
		t.Skipf("needs miniredis: %s", reason)
	}
}

// Stop makes the instance unreachable to simulate an outage. Only valid on
// miniredis — guard with SkipIfReal first.
func (s *Server) Stop(t testing.TB) {
	t.Helper()
	if s.mini == nil {
		t.Fatal("redistest: Stop requires miniredis; call SkipIfReal first")
	}
	s.mini.Close()
}
