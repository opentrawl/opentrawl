// Package flags carries the shared crawler CLI flag contracts, so every
// crawler enforces them one way instead of hand-rolling a copy.
package flags

import "errors"

// The one --limit contract (docs/contract.md): --limit N is honored exactly as
// given with no hidden cap, --all returns everything, a limit below 1 is a
// usage error, and --all combined with an explicit --limit is a usage error.
// Callers wrap these in their own usage-error type so the exit code and log
// line stay the crawler's own.
var (
	ErrLimitBelowOne = errors.New("--limit must be at least 1")
	ErrLimitAndAll   = errors.New("use either --all or --limit")
)

// Limit resolves the --limit / --all pair to the number of rows to return
// under the one --limit contract. limitSet reports whether --limit was given
// explicitly rather than defaulted, so --all and a real --limit can be refused
// instead of silently guessing. The returned count is 0 when the caller should
// return everything.
func Limit(n int, limitSet, all bool) (int, error) {
	if all {
		if limitSet {
			return 0, ErrLimitAndAll
		}
		return 0, nil
	}
	if n < 1 {
		return 0, ErrLimitBelowOne
	}
	return n, nil
}
