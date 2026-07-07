// Package whomatch provides the shared resolver matching rules for crawler
// "who" commands.
//
// Matching is deliberately generous for resolver output and exact for the
// filter that consumes a resolved candidate. The rank ladder is:
//
//	RankExact > RankPrefix > RankSubstring > RankCloseSpelling
//
// Higher Rank values are better. Close spelling uses edit distance on compact,
// folded strings, with one adjacent transposition counting as one edit. Both
// sides must be at least 3 runes and have the same first letter. The maximum
// distance is 1 when the longer side is 3 to 8 runes, 2 when it is 9 to 12
// runes, and 3 when it is longer.
//
// The rules.md §1.5 structural carve-out for this package — the match ladder
// above and display-name picking — is documented once, on BestDisplayName.
package whomatch

import (
	"strings"
	"time"
	"unicode"
)

type Rank int

const (
	RankCloseSpelling Rank = iota + 1
	RankSubstring
	RankPrefix
	RankExact
)

type Candidate struct {
	Who         string
	Identifiers []string
	Aliases     []string
	LastSeen    time.Time
	Messages    int64

	rank Rank
}

func (r Rank) BetterThan(other Rank) bool {
	return r > other
}

func (r Rank) String() string {
	switch r {
	case RankExact:
		return "exact"
	case RankPrefix:
		return "prefix"
	case RankSubstring:
		return "substring"
	case RankCloseSpelling:
		return "close_spelling"
	default:
		return "none"
	}
}

func (c Candidate) MatchRank(query string) (Rank, bool) {
	aliases := make([]string, 0, 1+len(c.Aliases)+len(c.Identifiers))
	aliases = append(aliases, c.Who)
	aliases = append(aliases, c.Aliases...)
	aliases = append(aliases, c.Identifiers...)
	return MatchRank(query, aliases)
}

func (c Candidate) Rank() (Rank, bool) {
	if c.rank == 0 {
		return 0, false
	}
	return c.rank, true
}

func Normalize(value string) string {
	return strings.Join(strings.Fields(fold(value)), " ")
}

func Compact(value string) string {
	var out strings.Builder
	for _, r := range fold(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func MatchRank(query string, aliases []string) (Rank, bool) {
	queryText := Normalize(query)
	queryCompact := Compact(query)
	if queryText == "" || queryCompact == "" {
		return 0, false
	}

	var best Rank
	for _, alias := range aliases {
		aliasText := Normalize(alias)
		aliasCompact := Compact(alias)
		if aliasText == "" || aliasCompact == "" {
			continue
		}
		rank, ok := aliasMatchRank(queryText, queryCompact, aliasText, aliasCompact)
		if ok && rank.BetterThan(best) {
			best = rank
		}
	}
	return best, best != 0
}

func IsIdentifierLike(value string, identifiers []string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	foldedValue := fold(value)
	for _, identifier := range identifiers {
		if foldedValue == fold(strings.TrimSpace(identifier)) {
			return true
		}
	}
	return !hasLetter(value)
}

func MergeSameName(candidates []Candidate) []Candidate {
	if len(candidates) == 0 {
		return nil
	}

	byName := map[string]int{}
	out := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.Who = cleanDisplay(candidate.Who)
		candidate.Identifiers = appendUniqueFolded(nil, candidate.Identifiers...)
		candidate.Aliases = appendUniqueFolded(nil, candidate.Aliases...)

		key := Normalize(candidate.Who)
		if key == "" {
			out = append(out, candidate)
			continue
		}
		index, ok := byName[key]
		if !ok {
			byName[key] = len(out)
			out = append(out, candidate)
			continue
		}
		mergeCandidate(&out[index], candidate)
	}
	return out
}

func aliasMatchRank(queryText, queryCompact, aliasText, aliasCompact string) (Rank, bool) {
	switch {
	case aliasText == queryText || aliasCompact == queryCompact:
		return RankExact, true
	case strings.HasPrefix(aliasText, queryText) || strings.HasPrefix(aliasCompact, queryCompact):
		return RankPrefix, true
	case strings.Contains(aliasText, queryText) || strings.Contains(aliasCompact, queryCompact):
		return RankSubstring, true
	case closeSpelling(queryCompact, aliasCompact):
		return RankCloseSpelling, true
	case closeWordSpelling(queryText, aliasText):
		return RankCloseSpelling, true
	default:
		return 0, false
	}
}

// closeWordSpelling matches word-wise: EVERY query word must closely
// match some alias word. Comparing the whole query against single
// alias words let "bob example" reach "Alice Example" through the
// shared surname.
func closeWordSpelling(query, value string) bool {
	queryWords := strings.Fields(query)
	if len(queryWords) == 0 {
		return false
	}
	aliasWords := strings.FieldsFunc(value, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, qw := range queryWords {
		qc := Compact(qw)
		matched := false
		for _, aw := range aliasWords {
			ac := Compact(aw)
			if qc == ac || closeSpelling(qc, ac) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func closeSpelling(query, value string) bool {
	queryLen := len([]rune(query))
	valueLen := len([]rune(value))
	if queryLen < 3 || valueLen < 3 {
		return false
	}
	queryFirst, queryHasLetter := firstLetter(query)
	valueFirst, valueHasLetter := firstLetter(value)
	if !queryHasLetter || !valueHasLetter {
		return false
	}
	if queryFirst != valueFirst {
		return false
	}

	distance := levenshteinDistance(query, value)
	longest := max(queryLen, valueLen)
	allowedDistance := closeSpellingDistance(longest)
	return distance <= allowedDistance || isSingleAdjacentTransposition(query, value, allowedDistance)
}

func closeSpellingDistance(longest int) int {
	switch {
	case longest <= 8:
		return 1
	case longest <= 12:
		return 2
	default:
		return 3
	}
}

func mergeCandidate(existing *Candidate, candidate Candidate) {
	identifiers := append([]string{}, existing.Identifiers...)
	identifiers = append(identifiers, candidate.Identifiers...)
	if shouldReplaceWho(existing.Who, candidate.Who, identifiers) {
		existing.Who = candidate.Who
	}
	existing.Identifiers = appendUniqueFolded(existing.Identifiers, candidate.Identifiers...)
	existing.Aliases = appendUniqueFolded(existing.Aliases, candidate.Aliases...)
	if candidate.LastSeen.After(existing.LastSeen) {
		existing.LastSeen = candidate.LastSeen
	}
	existing.Messages += candidate.Messages
	if candidate.rank.BetterThan(existing.rank) {
		existing.rank = candidate.rank
	}
}

func shouldReplaceWho(existing, candidate string, identifiers []string) bool {
	existing = cleanDisplay(existing)
	candidate = cleanDisplay(candidate)
	if candidate == "" {
		return false
	}
	if existing == "" {
		return true
	}
	if IsIdentifierLike(existing, identifiers) && !IsIdentifierLike(candidate, identifiers) {
		return true
	}
	return displayCaseQuality(candidate) > displayCaseQuality(existing)
}

func displayCaseQuality(value string) int {
	hasUpper := false
	hasLower := false
	for _, r := range value {
		if unicode.IsUpper(r) {
			hasUpper = true
		}
		if unicode.IsLower(r) {
			hasLower = true
		}
	}
	switch {
	case hasUpper && hasLower:
		return 2
	case hasLower:
		return 1
	default:
		return 0
	}
}

func appendUniqueFolded(out []string, values ...string) []string {
	seen := map[string]struct{}{}
	for _, value := range out {
		seen[fold(strings.TrimSpace(value))] = struct{}{}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := fold(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func cleanDisplay(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func fold(value string) string {
	var out strings.Builder
	for _, r := range value {
		out.WriteRune(foldRune(r))
	}
	return out.String()
}

func foldRune(r rune) rune {
	folded := unicode.ToLower(r)
	for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
		if lower := unicode.ToLower(next); lower < folded {
			folded = lower
		}
	}
	return folded
}

func hasLetter(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func firstLetter(value string) (rune, bool) {
	for _, r := range value {
		if unicode.IsLetter(r) {
			return foldRune(r), true
		}
	}
	return 0, false
}

func isSingleAdjacentTransposition(left, right string, allowedDistance int) bool {
	if allowedDistance < 1 {
		return false
	}
	a := []rune(left)
	b := []rune(right)
	if len(a) != len(b) {
		return false
	}

	firstDiff := -1
	for i := range a {
		if a[i] == b[i] {
			continue
		}
		if firstDiff != -1 {
			return i == firstDiff+1 &&
				a[firstDiff] == b[i] &&
				a[i] == b[firstDiff] &&
				equalRunes(a[i+1:], b[i+1:])
		}
		firstDiff = i
	}
	return false
}

func equalRunes(left, right []rune) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func levenshteinDistance(left, right string) int {
	a := []rune(left)
	b := []rune(right)
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i, ar := range a {
		current[0] = i + 1
		for j, br := range b {
			cost := 0
			if ar != br {
				cost = 1
			}
			current[j+1] = min(
				current[j]+1,
				previous[j+1]+1,
				previous[j]+cost,
			)
		}
		previous, current = current, previous
	}
	return previous[len(b)]
}
