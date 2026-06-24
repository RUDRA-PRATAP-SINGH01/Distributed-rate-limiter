package idempotency

import "errors"

var (
	ErrStoreUnavailable = errors.New("idempotency store unavailable")
	ErrKeyTooLong       = errors.New("idempotency key exceeds maximum length")
	ErrBodyTooLarge     = errors.New("request body exceeds idempotency limit")
)

const MaxKeyLength = 255
