// Package flags carries the shared trawler CLI flag contracts, so every
// trawler enforces them one way instead of hand-rolling a copy.
package flags

import "github.com/opentrawl/opentrawl/trawlkit/render"

// Limit resolves the number of rows to return under the one --limit contract
// (docs/contract.md): --limit N is honored exactly as given with no hidden
// cap, and a limit below 1 is a usage error.
func Limit(n int, limitSet bool) (int, error) {
	if n < 1 {
		return 0, render.LimitMustBeAtLeastOne()
	}
	return n, nil
}
