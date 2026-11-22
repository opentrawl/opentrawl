package whomatch

import (
	"sort"
	"strings"
)

// RankedMatch pairs a provider-native person candidate with its shared match
// rank and the match inputs used for tie-breaking.
type RankedMatch[T any] struct {
	Item      T
	Rank      Rank
	Candidate Candidate
}

// RankMatches scores each item against query using extract and keeps only items
// that match.
func RankMatches[T any](query string, items []T, extract func(T) Candidate) []RankedMatch[T] {
	if Normalize(query) == "" {
		return nil
	}
	matches := make([]RankedMatch[T], 0, len(items))
	for _, item := range items {
		candidate := extract(item)
		rank, matched := candidate.MatchRank(query)
		if !matched {
			continue
		}
		matches = append(matches, RankedMatch[T]{
			Item:      item,
			Rank:      rank,
			Candidate: candidate,
		})
	}
	return matches
}

// RankAndSortMatches scores items against query and orders matches by Rank
// descending, LastSeen descending, Messages descending, and Who ascending.
func RankAndSortMatches[T any](query string, items []T, extract func(T) Candidate) []RankedMatch[T] {
	matches := RankMatches(query, items, extract)
	sort.SliceStable(matches, func(i, j int) bool {
		left := matches[i]
		right := matches[j]
		if left.Rank != right.Rank {
			return left.Rank.BetterThan(right.Rank)
		}
		if !left.Candidate.LastSeen.Equal(right.Candidate.LastSeen) {
			return left.Candidate.LastSeen.After(right.Candidate.LastSeen)
		}
		if left.Candidate.Messages != right.Candidate.Messages {
			return left.Candidate.Messages > right.Candidate.Messages
		}
		return strings.ToLower(left.Candidate.Who) < strings.ToLower(right.Candidate.Who)
	})
	return matches
}

// PreferDirectMatches keeps only direct (exact, prefix, or substring) matches
// when any exist, falling back to close-spelling matches otherwise.
func PreferDirectMatches[T any](matches []RankedMatch[T]) []RankedMatch[T] {
	direct := make([]RankedMatch[T], 0, len(matches))
	for _, match := range matches {
		if match.Rank != RankCloseSpelling {
			direct = append(direct, match)
		}
	}
	if len(direct) > 0 {
		return direct
	}
	return matches
}

// PrioritizeMatchingIdentifiers orders identifiers that match query first by
// descending match rank while preserving original order among ties.
func PrioritizeMatchingIdentifiers(query string, identifiers []string) []string {
	if len(identifiers) == 0 {
		return identifiers
	}
	type rankedIdentifier struct {
		value   string
		rank    Rank
		matched bool
		index   int
	}
	ranked := make([]rankedIdentifier, 0, len(identifiers))
	anyMatched := false
	for index, identifier := range identifiers {
		rank, matched := MatchRank(query, []string{identifier})
		if matched {
			anyMatched = true
		}
		ranked = append(ranked, rankedIdentifier{
			value:   identifier,
			rank:    rank,
			matched: matched,
			index:   index,
		})
	}
	if !anyMatched {
		return identifiers
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].matched != ranked[j].matched {
			return ranked[i].matched
		}
		if ranked[i].matched && ranked[i].rank != ranked[j].rank {
			return ranked[i].rank.BetterThan(ranked[j].rank)
		}
		return ranked[i].index < ranked[j].index
	})
	out := make([]string, 0, len(ranked))
	for _, identifier := range ranked {
		out = append(out, identifier.value)
	}
	return out
}
