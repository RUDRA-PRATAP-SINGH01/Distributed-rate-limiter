// Package chaos hosts CI-gated resilience contract tests.
//
// Run with: go test -tags=chaos ./chaos/...
// These tests are skipped in the default go test ./... path so unit CI stays
// fast and does not require Docker.
package chaos
