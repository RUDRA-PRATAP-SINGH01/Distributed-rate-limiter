package circuitbreaker

import "context"

// Store is the persistence/backend for circuit state.
//
// Use LocalStore when the protected dependency is Redis itself (state must not
// live inside the failing dependency). Use RedisStore for fleet-shared targets
// such as gateways or the central limiter.
type Store interface {
	Allow(ctx context.Context, target string) (AllowResult, error)
	Record(ctx context.Context, target string, input RecordInput) (Snapshot, error)
	GetState(ctx context.Context, target string) (Snapshot, error)
	Reset(ctx context.Context, target string) error
	ListTargets(ctx context.Context) ([]Snapshot, error)
}
