package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	ckstore "github.com/openclaw/crawlkit/store"
)

const (
	RefPrefix       = "gmail:msg/"
	LegacyRefPrefix = "gogcrawl:msg/"
)

func countTable(ctx context.Context, db *sql.DB, table string) (int64, error) {
	var count int64
	if err := db.QueryRowContext(ctx, `select count(*) from `+table).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func ftsQuery(query string) string {
	value := ckstore.FTS5TokenQuery(query)
	if value != "" {
		return value
	}
	return ckstore.FTS5Phrase(query)
}

func labelsJSON(labels []string) string {
	data, err := json.Marshal(labels)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func parseLabels(value string) []string {
	var labels []string
	if err := json.Unmarshal([]byte(value), &labels); err != nil {
		return nil
	}
	return labels
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func formatArchiveTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Local().Format(time.RFC3339)
}

func parseRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	id, ok := strings.CutPrefix(ref, RefPrefix)
	if !ok {
		id, ok = strings.CutPrefix(ref, LegacyRefPrefix)
	}
	if !ok || strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("ref must look like %s<gmail-message-id>", RefPrefix)
	}
	return strings.TrimSpace(id), nil
}

func displaySender(name, address string, ownerEmails map[string]struct{}) string {
	if isOwnerEmail(address, ownerEmails) {
		if email := normalizeEmail(address); email != "" {
			return "me (" + email + ")"
		}
		return "me"
	}
	if strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	if strings.TrimSpace(address) != "" {
		return strings.TrimSpace(address)
	}
	return "unknown sender"
}

func plainSnippet(query, subject, body string) string {
	// Keep this local until crawlkit grows the canonical helper: plain
	// fragment, flattened whitespace, no FTS marker brackets.
	text := flattenWhitespace(strings.TrimSpace(subject + " " + body))
	if text == "" {
		return ""
	}
	needle := firstSearchTerm(query)
	start := 0
	if needle != "" {
		if idx := strings.Index(strings.ToLower(text), strings.ToLower(needle)); idx >= 0 {
			start = snippetRuneStart(text, idx, 60)
		}
	}
	const limit = 180
	runes := []rune(text)
	frontTruncated := nonBoundaryCut(runes, start)
	if frontTruncated {
		start = nextTokenBoundary(runes, start)
	}
	end := len(runes)
	if len(runes)-start > limit {
		end = start + limit
	}
	if nonBoundaryCut(runes, end) {
		end = previousTokenBoundary(runes, start, end)
	}
	snippet := strings.TrimSpace(string(runes[start:end]))
	if snippet == "" {
		return ""
	}
	if frontTruncated {
		snippet = "…" + snippet
	}
	if end < len(runes) {
		snippet += "…"
	}
	return snippet
}

func flattenWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func firstSearchTerm(query string) string {
	for _, field := range strings.Fields(query) {
		field = strings.Trim(field, `"'()`)
		if field != "" {
			return field
		}
	}
	return ""
}

func snippetRuneStart(text string, byteIndex, contextRunes int) int {
	start := 0
	for byteOffset := range text {
		if byteOffset >= byteIndex {
			break
		}
		start++
	}
	start -= contextRunes
	if start < 0 {
		return 0
	}
	return start
}

func nonBoundaryCut(runes []rune, cut int) bool {
	if cut <= 0 || cut >= len(runes) {
		return false
	}
	return !unicode.IsSpace(runes[cut-1]) && !unicode.IsSpace(runes[cut])
}

func nextTokenBoundary(runes []rune, cut int) int {
	for cut < len(runes) && !unicode.IsSpace(runes[cut]) {
		cut++
	}
	for cut < len(runes) && unicode.IsSpace(runes[cut]) {
		cut++
	}
	return cut
}

func previousTokenBoundary(runes []rune, start, cut int) int {
	for cut > start && !unicode.IsSpace(runes[cut-1]) {
		cut--
	}
	for cut > start && unicode.IsSpace(runes[cut-1]) {
		cut--
	}
	if cut <= start {
		return minInt(len(runes), start)
	}
	return cut
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
