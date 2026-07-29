package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode"
)

const (
	fts5MatchStart                                                                 = "\ue000"
	fts5MatchEnd                                                                   = "\ue001"
	maximumFTS5SnippetTokenCount                                                   = 64
	maximumPlainTextFTS5SnippetRuneCountForTwoLinesAtMaximumSupportedTerminalWidth = 1000
)

// FTS5TextRun is a section of text returned by SQLite's FTS5 snippet function.
type FTS5TextRun struct {
	Text    string
	Matched bool
}

// FTS5MarkedSearchResultSnippetSQLExpression returns the SQLite expression
// that extracts the largest supported marked FTS5 snippet for one column.
func FTS5MarkedSearchResultSnippetSQLExpression(fts5TableName string, fts5ColumnIndex int) string {
	return fmt.Sprintf(
		"snippet(%s, %d, char(57344), char(57345), '…', %d)",
		QuoteIdent(fts5TableName),
		fts5ColumnIndex,
		maximumFTS5SnippetTokenCount,
	)
}

// ParseFTS5MarkedText splits text marked with U+E000 and U+E001 by SQLite's
// FTS5 snippet function. It returns nil when the text contains no complete
// marked match.
func ParseFTS5MarkedText(value string) []FTS5TextRun {
	if !strings.Contains(value, fts5MatchStart) {
		return nil
	}
	var runs []FTS5TextRun
	for value != "" {
		startIndex := strings.Index(value, fts5MatchStart)
		if startIndex < 0 {
			runs = appendFTS5TextRun(runs, value, false)
			break
		}
		runs = appendFTS5TextRun(runs, value[:startIndex], false)
		value = value[startIndex+len(fts5MatchStart):]
		endIndex := strings.Index(value, fts5MatchEnd)
		if endIndex < 0 {
			return nil
		}
		runs = appendFTS5TextRun(runs, value[:endIndex], true)
		value = value[endIndex+len(fts5MatchEnd):]
	}
	return runs
}

func appendFTS5TextRun(runs []FTS5TextRun, text string, matched bool) []FTS5TextRun {
	if text == "" {
		return runs
	}
	if len(runs) > 0 && runs[len(runs)-1].Matched == matched {
		runs[len(runs)-1].Text += text
		return runs
	}
	return append(runs, FTS5TextRun{Text: text, Matched: matched})
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

func FTS5TermsInTextAndMediaColumns(value string) (string, error) {
	searchTerms, err := FTS5Terms(value, "")
	if err != nil {
		return "", err
	}
	return "{text media} : (" + searchTerms + ")", nil
}

// FTS5TokenQuery converts arbitrary user input into quoted FTS5 tokens joined
// by implicit AND, discarding punctuation that could be parsed as operators.
// It returns an empty string when no searchable tokens remain.
func FTS5TokenQuery(value string) string {
	terms := make([]string, 0)
	var token strings.Builder
	flush := func() {
		if token.Len() == 0 {
			return
		}
		terms = append(terms, FTS5Phrase(token.String()))
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
	return strings.Join(terms, " ")
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
	if len(runes) <= maximumPlainTextFTS5SnippetRuneCountForTwoLinesAtMaximumSupportedTerminalWidth {
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
	if len(text) <= maximumPlainTextFTS5SnippetRuneCountForTwoLinesAtMaximumSupportedTerminalWidth {
		return 0, len(text)
	}
	start := 0
	if matched {
		matchLen := matchEnd - matchStart
		if matchLen < 0 {
			matchLen = 0
		}
		start = matchStart - (maximumPlainTextFTS5SnippetRuneCountForTwoLinesAtMaximumSupportedTerminalWidth-matchLen)/2
		if start < 0 {
			start = 0
		}
		if start+maximumPlainTextFTS5SnippetRuneCountForTwoLinesAtMaximumSupportedTerminalWidth > len(text) {
			start = len(text) - maximumPlainTextFTS5SnippetRuneCountForTwoLinesAtMaximumSupportedTerminalWidth
		}
		start = fts5SnippetStartBoundary(text, start, matchStart)
	}
	end := start + maximumPlainTextFTS5SnippetRuneCountForTwoLinesAtMaximumSupportedTerminalWidth
	if end > len(text) {
		end = len(text)
	}
	if matched {
		end = fts5SnippetEndBoundary(text, end, matchEnd)
	} else {
		end = fts5SnippetEndBoundary(text, end, 0)
	}
	if end <= start {
		end = start + maximumPlainTextFTS5SnippetRuneCountForTwoLinesAtMaximumSupportedTerminalWidth
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
