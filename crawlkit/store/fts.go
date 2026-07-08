package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode"
)

const fts5SnippetMaxRunes = 120

const FTS5TrigramTokenizer = "tokenize='trigram'"

type FTS5QueryMode string

const (
	FTS5QueryMatch FTS5QueryMode = "match"
	FTS5QueryLike  FTS5QueryMode = "like"
)

type FTS5QueryPlan struct {
	Mode         FTS5QueryMode
	Match        string
	LikePatterns []string
}

func FTS5Phrase(value string) string {
	return `"` + strings.ReplaceAll(strings.TrimSpace(value), `"`, `""`) + `"`
}

// FTS5Terms converts whitespace-delimited user input into quoted FTS5 terms.
// Operator may be empty (implicit AND), AND, or OR.
func FTS5Terms(value, operator string) (string, error) {
	operator = strings.ToUpper(strings.TrimSpace(operator))
	if operator != "" && operator != "AND" && operator != "OR" {
		return "", fmt.Errorf("unsupported FTS5 operator %q", operator)
	}
	terms := strings.Fields(value)
	if len(terms) == 0 {
		return FTS5Phrase(""), nil
	}
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		quoted = append(quoted, FTS5Phrase(term))
	}
	separator := " "
	if operator != "" {
		separator = " " + operator + " "
	}
	return strings.Join(quoted, separator), nil
}

// FTS5TokenQuery converts arbitrary user input into quoted FTS5 tokens joined
// by implicit AND, discarding punctuation that could be parsed as operators.
// It returns an empty string when no searchable tokens remain.
func FTS5TokenQuery(value string) string {
	tokens := fts5QueryTokens(value)
	terms := make([]string, 0, len(tokens))
	for _, token := range tokens {
		terms = append(terms, FTS5Phrase(token))
	}
	return strings.Join(terms, " ")
}

// RouteFTS5Query returns the query shape for the fleet trigram FTS5
// convention. Trigram MATCH has no token for a CJK term shorter than three
// runes, so a query with any such term uses LIKE patterns against the same FTS
// table columns. Every other query uses MATCH. The rule is structural: it only
// checks Unicode CJK codepoint ranges, with no language detection or scoring.
func RouteFTS5Query(value string) FTS5QueryPlan {
	tokens := fts5QueryTokens(value)
	if len(tokens) == 0 {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return FTS5QueryPlan{Mode: FTS5QueryMatch}
		}
		return FTS5QueryPlan{
			Mode:         FTS5QueryLike,
			LikePatterns: []string{"%" + EscapeLike(trimmed) + "%"},
		}
	}
	if hasShortCJKTerm(tokens) {
		patterns := make([]string, 0, len(tokens))
		for _, token := range tokens {
			patterns = append(patterns, "%"+EscapeLike(token)+"%")
		}
		return FTS5QueryPlan{Mode: FTS5QueryLike, LikePatterns: patterns}
	}
	terms := make([]string, 0, len(tokens))
	for _, token := range tokens {
		terms = append(terms, FTS5Phrase(token))
	}
	return FTS5QueryPlan{Mode: FTS5QueryMatch, Match: strings.Join(terms, " ")}
}

func fts5QueryTokens(value string) []string {
	terms := make([]string, 0)
	var token strings.Builder
	flush := func() {
		if token.Len() == 0 {
			return
		}
		terms = append(terms, token.String())
		token.Reset()
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '_' {
			token.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return terms
}

func hasShortCJKTerm(tokens []string) bool {
	for _, token := range tokens {
		cjk := 0
		for _, r := range token {
			if isCJKRune(r) {
				cjk++
			}
		}
		if cjk > 0 && cjk < 3 {
			return true
		}
	}
	return false
}

func isCJKRune(r rune) bool {
	switch {
	case r >= 0x3400 && r <= 0x4DBF:
		return true
	case r >= 0x4E00 && r <= 0x9FFF:
		return true
	case r >= 0xF900 && r <= 0xFAFF:
		return true
	case r >= 0x20000 && r <= 0x2FA1F:
		return true
	case r >= 0x3040 && r <= 0x30FF:
		return true
	case r >= 0x31F0 && r <= 0x31FF:
		return true
	case r >= 0xFF66 && r <= 0xFF9F:
		return true
	case r >= 0x1100 && r <= 0x11FF:
		return true
	case r >= 0x3130 && r <= 0x318F:
		return true
	case r >= 0xA960 && r <= 0xA97F:
		return true
	case r >= 0xAC00 && r <= 0xD7FF:
		return true
	case r >= 0xD7B0 && r <= 0xD7FF:
		return true
	default:
		return false
	}
}

// FTS5Snippet returns a plain text search fragment centred near a query match.
// It uses no markers: the value is already a fragment, and callers can open
// the result ref when they need the full item.
func FTS5Snippet(text, query string) string {
	text = collapseFTS5SnippetWhitespace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= fts5SnippetMaxRunes {
		return text
	}
	matchStart, matchEnd, ok := fts5SnippetMatch(runes, query)
	if !ok {
		matchStart, matchEnd = 0, 0
	}
	start, end := fts5SnippetWindow(runes, matchStart, matchEnd, ok)
	return strings.TrimSpace(string(runes[start:end]))
}

func collapseFTS5SnippetWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func fts5SnippetMatch(text []rune, query string) (int, int, bool) {
	terms := fts5SnippetTerms(query)
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
			if !fts5SnippetTermMatch(lowerText[start:end], term) {
				continue
			}
			if !fts5SnippetTokenBoundary(lowerText, start, end) {
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

func fts5SnippetTerms(query string) [][]rune {
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
		if isFTS5SnippetTokenRune(r) {
			token = append(token, r)
			continue
		}
		flush()
	}
	flush()
	return terms
}

func fts5SnippetTermMatch(text, term []rune) bool {
	for i := range term {
		if text[i] != term[i] {
			return false
		}
	}
	return true
}

func fts5SnippetTokenBoundary(text []rune, start, end int) bool {
	if start > 0 && isFTS5SnippetTokenRune(text[start-1]) {
		return false
	}
	return end >= len(text) || !isFTS5SnippetTokenRune(text[end])
}

func isFTS5SnippetTokenRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '_'
}

func fts5SnippetWindow(text []rune, matchStart, matchEnd int, matched bool) (int, int) {
	if len(text) <= fts5SnippetMaxRunes {
		return 0, len(text)
	}
	start := 0
	if matched {
		matchLen := matchEnd - matchStart
		if matchLen < 0 {
			matchLen = 0
		}
		start = matchStart - (fts5SnippetMaxRunes-matchLen)/2
		if start < 0 {
			start = 0
		}
		if start+fts5SnippetMaxRunes > len(text) {
			start = len(text) - fts5SnippetMaxRunes
		}
		start = fts5SnippetStartBoundary(text, start, matchStart)
	}
	end := start + fts5SnippetMaxRunes
	if end > len(text) {
		end = len(text)
	}
	if matched {
		end = fts5SnippetEndBoundary(text, end, matchEnd)
	} else {
		end = fts5SnippetEndBoundary(text, end, 0)
	}
	if end <= start {
		end = start + fts5SnippetMaxRunes
		if end > len(text) {
			end = len(text)
		}
	}
	return start, end
}

func fts5SnippetStartBoundary(text []rune, start, matchStart int) int {
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

func fts5SnippetEndBoundary(text []rune, end, matchEnd int) int {
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

type contextExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func OptimizeFTS5(ctx context.Context, db contextExecer, table string) error {
	table = strings.TrimSpace(table)
	if table == "" || strings.ContainsAny(table, "\"\x00") {
		return fmt.Errorf("invalid FTS5 table %q", table)
	}
	quoted := QuoteIdent(table)
	if _, err := db.ExecContext(ctx, "insert into "+quoted+"("+quoted+") values('optimize')"); err != nil {
		return fmt.Errorf("optimize FTS5 table %s: %w", table, err)
	}
	return nil
}
