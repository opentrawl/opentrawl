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
// Every rule is structural, applied in order:
//  1. Angle-bracket spans are stripped ("Avery E <avery@example.com>" becomes
//     "Avery E") and spellings that become identical pool their counts. A
//     spelling that strips to nothing is dropped.
//  2. Highest count wins.
//  3. A spelling that is not identifier-like (IsIdentifierLike) beats one
//     that is.
//  4. Mixed case beats all-lowercase beats ALL-CAPS/no-letters.
//  5. Fewer runes win.
//  6. Alphabetical, case-insensitively, then exactly.
//
// rules.md §1.5 carve-out, documented once here for every crawler that
// routes through this picker: agents retry who-resolution against this pick,
// so the same input must give the same output every time — query-time
// stability is a contract property. The rules above operate on string
// structure only (counts, identifier equality, bracket spans, letter case,
// length); none judges what a "good" name means — that is a model's call at
// a different layer. A model call here is architecturally wrong on latency:
// this runs inside every interactive who / search --who resolution. The
// precompute-at-sync-time alternative was considered and rejected: the
// candidate set is assembled at query time by merging records across events
// and messages (union-find over shared identifiers), so a per-row sync-time
// pick never sees the full spelling set it must choose from.
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
