// Package flags carries the shared crawler CLI flag contracts, so every
// crawler enforces them one way instead of hand-rolling a copy.
package flags

import "errors"

// The one --limit contract (docs/contract.md): --limit N is honored exactly as
// given with no hidden cap, and a limit below 1 is a usage error.
// Callers wrap these in their own usage-error type so the exit code and log
// line stay the crawler's own.
var (
	ErrLimitBelowOne = errors.New("--limit must be at least 1")
)

// Limit resolves the number of rows to return under the one --limit contract.
func Limit(n int, limitSet bool) (int, error) {
	if n < 1 {
		return 0, ErrLimitBelowOne
	}
	return n, nil
}
