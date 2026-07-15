package whomatch

import (
	"sort"
	"strings"
)

// BestDisplayName picks which observed spelling of a person's name to
// display. names maps each observed spelling to how many times it was seen;
// identifiers are the person's known identifiers (emails, phone numbers,
// handles). It returns "" when no spelling survives structural cleanup;
// callers fall back to their own identifier ordering.
//
// This package has 2 deterministic rule ladders.
//
// Match ranking is structural and ordered:
//  1. Exact match on normalised text or compact text.
//  2. Prefix match on normalised text or compact text.
//  3. Substring match on normalised text or compact text.
//  4. Close spelling on compact folded strings, or word-by-word compact
//     folded strings.
//
// Close spelling is deliberately narrow. Both sides must contain at least 3
// runes and start with the same first letter after case folding and Latin
// base-letter folding. The edit-distance allowance is 1 for 3 to 8 runes, 2
// for 9 to 12 runes, and 3 above 12 runes. One adjacent transposition also
// counts as a close spelling match. That accepts the classic swapped-letter
// typo, such as jhon -> john, only when the strings have equal length, the
// first-letter gate has already passed, and exactly one adjacent pair is
// swapped.
//
// Display-name picking is structural and ordered:
//  1. Angle-bracket spans are stripped ("Avery Example <avery@example.com>"
//     becomes "Avery Example") and spellings that become identical pool their counts. A
//     spelling that strips to nothing is dropped.
//  2. Highest count wins.
//  3. A spelling that is not identifier-like (IsIdentifierLike) beats one
//     that is.
//  4. Mixed case beats all-lowercase beats ALL-CAPS/no-letters.
//  5. Fewer runes win.
//  6. Alphabetical, case-insensitively, then exactly.
//
// This deterministic boundary is documented once here for every crawler that
// routes through whomatch: agents retry who-resolution against these rules, so the
// same input must give the same output every time. These rules operate on
// string structure only: exact containment, compact character distance, one
// adjacent swap, counts, identifier equality, bracket spans, case, and length.
// None judges what a "good" name means; that is a model's call at a different
// layer. A model call here is architecturally wrong on latency because these
// rules run inside every interactive who / search --who resolution. The
// precompute-at-sync-time alternative was considered and rejected: queries and
// merged candidate sets are assembled at query time across events and messages
// by shared identifiers, so a per-row sync-time pick never sees the full input
// it must rank or choose from.
func BestDisplayName(names map[string]int, identifiers []string) string {
	counts := map[string]int{}
	for value, count := range names {
		value = cleanDisplay(stripAngleSpans(value))
		if value == "" {
			continue
		}
		counts[value] += count
	}
	if len(counts) == 0 {
		return ""
	}
	type spelling struct {
		value          string
		count          int
		identifierLike bool
	}
	spellings := make([]spelling, 0, len(counts))
	for value, count := range counts {
		spellings = append(spellings, spelling{
			value:          value,
			count:          count,
			identifierLike: IsIdentifierLike(value, identifiers),
		})
	}
	sort.Slice(spellings, func(i, j int) bool {
		left, right := spellings[i], spellings[j]
		if left.count != right.count {
			return left.count > right.count
		}
		if left.identifierLike != right.identifierLike {
			return !left.identifierLike
		}
		leftCase, rightCase := displayCaseQuality(left.value), displayCaseQuality(right.value)
		if leftCase != rightCase {
			return leftCase > rightCase
		}
		leftLen, rightLen := len([]rune(left.value)), len([]rune(right.value))
		if leftLen != rightLen {
			return leftLen < rightLen
		}
		leftLower, rightLower := strings.ToLower(left.value), strings.ToLower(right.value)
		if leftLower != rightLower {
			return leftLower < rightLower
		}
		return left.value < right.value
	})
	return spellings[0].value
}

// stripAngleSpans removes every closed <...> span: display strings routinely
// carry "Name <email>" cruft from calendar and mail headers. An unmatched
// bracket is kept verbatim.
func stripAngleSpans(value string) string {
	for {
		open := strings.Index(value, "<")
		if open < 0 {
			return value
		}
		length := strings.Index(value[open:], ">")
		if length < 0 {
			return value
		}
		value = value[:open] + value[open+length+1:]
	}
}
