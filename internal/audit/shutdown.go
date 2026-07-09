package audit

import (
	"context"
)

type storeState int

const (
	stateRunning storeState = iota
	stateShuttingDown
	stateStopped
)

// Shutdown drains accepted async audit events and stops workers. It is safe to
// call repeatedly and concurrently; queue closure happens exactly once.
//
// Returns nil when all workers have terminated. On context deadline, returns
// ctx.Err() while workers may still be running — callers must not close the
// shared Redis client until RedisCloseSafe() is true. A later Shutdown call
// with a fresh context can resume waiting for worker termination.
func (s *Store) Shutdown(ctx context.Context) error {
	s.shutMu.Lock()
	defer s.shutMu.Unlock()

	s.beginShutdown()

	s.mu.Lock()
	if s.state == stateStopped {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	if !s.cfg.Enabled || !s.cfg.Async || s.cfg.Workers <= 0 {
		s.mu.Lock()
		s.state = stateStopped
		s.mu.Unlock()
		return nil
	}

	return s.waitWorkers(ctx)
}

// RedisCloseSafe reports whether the shared Redis client can be closed without
// racing live audit workers.
func (s *Store) RedisCloseSafe() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.cfg.Enabled {
		return true
	}
	if !s.cfg.Async || s.cfg.Workers <= 0 {
		return true
	}
	return s.state == stateStopped
}

func (s *Store) beginShutdown() {
	s.shutdownBeginOnce.Do(func() {
		if !s.cfg.Enabled {
			s.mu.Lock()
			s.state = stateStopped
			s.mu.Unlock()
			return
		}

		s.ensureWorkers()

		s.mu.Lock()
		defer s.mu.Unlock()
		if s.state == stateStopped {
			return
		}
		s.state = stateShuttingDown
		if s.queue != nil {
			close(s.queue)
		}
	})
}

func (s *Store) waitWorkers(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.mu.Lock()
		s.state = stateStopped
		s.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Store) ensureWorkers() {
	s.workersOnce.Do(func() {
		if !s.cfg.Async || s.cfg.Workers <= 0 {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.queue != nil {
			return
		}
		s.queue = make(chan RecordInput, s.cfg.QueueSize)
		for i := 0; i < s.cfg.Workers; i++ {
			s.wg.Add(1)
			go s.worker()
		}
	})
}

func (s *Store) worker() {
	defer s.wg.Done()
	for in := range s.queue {
		_, _ = s.record(context.Background(), in)
	}
}
