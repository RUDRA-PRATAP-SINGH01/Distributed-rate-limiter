package idempotency

// Status is the lifecycle state of an idempotency record in Redis.
type Status string

const (
	StatusProcessing Status = "processing"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
)

// ClaimResult classifies the outcome of an atomic claim attempt.
type ClaimResult int

const (
	ResultClaimed ClaimResult = iota
	ResultReplay
	ResultInProgress
	ResultHashMismatch
	ResultError
)

// ClaimResponse is returned by Store.Claim.
type ClaimResponse struct {
	Result       ClaimResult
	RetryAfterMs int64
	HTTPStatus   int
	Headers      map[string]string
	Body         []byte
}

// CompleteRequest stores a finished upstream response.
type CompleteRequest struct {
	Scope      string
	Key        string
	HTTPStatus int
	Headers    map[string]string
	Body       []byte
}

// Config tunes TTLs and size limits for the idempotency store.
type Config struct {
	LockTTL         int64 // processing lease (ms)
	CompletedTTL    int64 // retention after completion (ms)
	MaxBodyBytes    int64
	InlineThreshold int // body stored in HASH below this size
	FailOpen        bool
}

// DefaultConfig returns production-oriented defaults (Stripe-style 24h retention).
func DefaultConfig() Config {
	return Config{
		LockTTL:         60_000,       // 60s processing lease
		CompletedTTL:    86_400_000,   // 24h
		MaxBodyBytes:    1_048_576,    // 1 MB
		InlineThreshold: 65_536,       // 64 KB inline in HASH
		FailOpen:        false,
	}
}
