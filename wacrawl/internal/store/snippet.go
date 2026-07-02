package store

import (
	"strings"
	"unicode"
)

const ftsSnippetMaxRunes = 120

// contractSnippet mirrors crawlkit/store.FTS5Snippet until wacrawl can move
// to the next crawlkit release that publishes the helper.
func contractSnippet(text, query string) string {
	text = collapseSnippetWhitespace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= ftsSnippetMaxRunes {
		return text
	}
	matchStart, matchEnd, ok := snippetMatch(runes, query)
	if !ok {
		matchStart, matchEnd = 0, 0
	}
	start, end := snippetWindow(runes, matchStart, matchEnd, ok)
	return strings.TrimSpace(string(runes[start:end]))
}

func collapseSnippetWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func snippetMatch(text []rune, query string) (int, int, bool) {
	terms := snippetTerms(query)
	if len(terms) == 0 {
		return 0, 0, false
	}
	lowerText := make([]rune, len(text))
	for i, r := range text {
		lowerText[i] = unicode.ToLower(r)
	}
	bestStart := len(text)
	bestEnd := 0
	for _, term := range terms {
		if len(term) == 0 || len(term) > len(lowerText) {
			continue
		}
		for start := 0; start <= len(lowerText)-len(term); start++ {
			end := start + len(term)
			if !snippetTermMatch(lowerText[start:end], term) {
				continue
			}
			if !snippetTokenBoundary(lowerText, start, end) {
				continue
			}
			if start < bestStart {
				bestStart = start
				bestEnd = end
			}
			break
		}
	}
	if bestStart == len(text) {
		return 0, 0, false
	}
	return bestStart, bestEnd, true
}

func snippetTerms(query string) [][]rune {
	terms := make([][]rune, 0)
	seen := map[string]struct{}{}
	var token []rune
	flush := func() {
		if len(token) == 0 {
			return
		}
		term := make([]rune, len(token))
		for i, r := range token {
			term[i] = unicode.ToLower(r)
		}
		key := string(term)
		if _, ok := seen[key]; !ok {
			terms = append(terms, term)
			seen[key] = struct{}{}
		}
		token = token[:0]
	}
	for _, r := range query {
		if isSnippetTokenRune(r) {
			token = append(token, r)
			continue
		}
		flush()
	}
	flush()
	return terms
}

func snippetTermMatch(text, term []rune) bool {
	for i := range term {
		if text[i] != term[i] {
			return false
		}
	}
	return true
}

func snippetTokenBoundary(text []rune, start, end int) bool {
	if start > 0 && isSnippetTokenRune(text[start-1]) {
		return false
	}
	return end >= len(text) || !isSnippetTokenRune(text[end])
}

func isSnippetTokenRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '_'
}

func snippetWindow(text []rune, matchStart, matchEnd int, matched bool) (int, int) {
	if len(text) <= ftsSnippetMaxRunes {
		return 0, len(text)
	}
	start := 0
	if matched {
		matchLen := matchEnd - matchStart
		if matchLen < 0 {
			matchLen = 0
		}
		start = matchStart - (ftsSnippetMaxRunes-matchLen)/2
		if start < 0 {
			start = 0
		}
		if start+ftsSnippetMaxRunes > len(text) {
			start = len(text) - ftsSnippetMaxRunes
		}
		start = snippetStartBoundary(text, start, matchStart)
	}
	end := start + ftsSnippetMaxRunes
	if end > len(text) {
		end = len(text)
	}
	if matched {
		end = snippetEndBoundary(text, end, matchEnd)
	} else {
		end = snippetEndBoundary(text, end, 0)
	}
	if end <= start {
		end = start + ftsSnippetMaxRunes
		if end > len(text) {
			end = len(text)
		}
	}
	return start, end
}

func snippetStartBoundary(text []rune, start, matchStart int) int {
	if start <= 0 || start >= len(text) || text[start-1] == ' ' {
		return start
	}
	for i := start; i < matchStart && i < len(text); i++ {
		if text[i] == ' ' {
			return i + 1
		}
	}
	for i := start - 1; i >= 0; i-- {
		if text[i] == ' ' {
			return i + 1
		}
	}
	return start
}

func snippetEndBoundary(text []rune, end, matchEnd int) int {
	if end >= len(text) || end <= 0 || text[end-1] == ' ' {
		return end
	}
	for i := end; i > matchEnd; i-- {
		if text[i-1] == ' ' {
			return i - 1
		}
	}
	return end
}
