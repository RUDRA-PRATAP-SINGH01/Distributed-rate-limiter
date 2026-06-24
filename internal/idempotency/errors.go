package idempotency

import "errors"

var (
	ErrStoreUnavailable = errors.New("idempotency store unavailable")
	ErrStaleFence       = errors.New("idempotency fence token mismatch — stale lease holder")
	ErrKeyTooLong       = errors.New("idempotency key exceeds maximum length")
	ErrBodyTooLarge     = errors.New("request body exceeds idempotency limit")
)

const MaxKeyLength = 255
