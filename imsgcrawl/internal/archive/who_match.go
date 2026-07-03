package archive

import (
	"strings"
	"unicode"
)

func whoCandidateDirectMatchRank(query string, candidate WhoCandidate) (int, bool) {
	values := append([]string{candidate.Who}, candidate.Identifiers...)
	best := whoMatchNone
	for _, value := range values {
		rank, ok := whoValueDirectMatchRank(query, value)
		if ok && rank < best {
			best = rank
		}
	}
	return best, best != whoMatchNone
}

func whoCandidateCloseMatchRank(query string, candidate WhoCandidate) (int, bool) {
	if whoValueCloseMatch(query, candidate.Who) {
		return whoMatchClose, true
	}
	return whoMatchNone, false
}

func whoValueDirectMatchRank(query, value string) (int, bool) {
	query = normalizeMatchValue(query)
	value = normalizeMatchValue(value)
	if query == "" || value == "" {
		return whoMatchNone, false
	}
	switch {
	case query == value:
		return whoMatchExact, true
	case strings.HasPrefix(value, query):
		return whoMatchPrefix, true
	case strings.Contains(value, query):
		return whoMatchContains, true
	default:
		return whoMatchNone, false
	}
}

func whoValueCloseMatch(query, value string) bool {
	query = normalizeMatchValue(query)
	value = normalizeMatchValue(value)
	if query == "" || value == "" {
		return false
	}
	return closeSpelling(query, value)
}

func closeSpelling(query, value string) bool {
	limit := closeSpellingLimit(query)
	if editDistance(query, value, limit) <= limit {
		return true
	}
	if strings.Contains(query, " ") {
		return false
	}
	for _, token := range strings.Fields(value) {
		if editDistance(query, token, limit) <= limit {
			return true
		}
	}
	return false
}

func closeSpellingLimit(value string) int {
	length := len([]rune(value))
	switch {
	case length <= 3:
		return 0
	case length <= 6:
		return 1
	case length <= 14:
		return 2
	default:
		return 3
	}
}

func editDistance(a, b string, maxDistance int) int {
	left := []rune(a)
	right := []rune(b)
	if delta := len(left) - len(right); delta > maxDistance || -delta > maxDistance {
		return maxDistance + 1
	}
	previous := make([]int, len(right)+1)
	current := make([]int, len(right)+1)
	for j := range previous {
		previous[j] = j
	}
	for i, lr := range left {
		current[0] = i + 1
		rowMin := current[0]
		for j, rr := range right {
			cost := 1
			if lr == rr {
				cost = 0
			}
			current[j+1] = minInt(previous[j+1]+1, current[j]+1, previous[j]+cost)
			if current[j+1] < rowMin {
				rowMin = current[j+1]
			}
		}
		if rowMin > maxDistance {
			return maxDistance + 1
		}
		previous, current = current, previous
	}
	return previous[len(right)]
}

func minInt(values ...int) int {
	out := values[0]
	for _, value := range values[1:] {
		if value < out {
			out = value
		}
	}
	return out
}

func normalizeMatchValue(value string) string {
	value = normalizeSearchWho(value)
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return unicode.ToLower(r)
	}, value)
}
