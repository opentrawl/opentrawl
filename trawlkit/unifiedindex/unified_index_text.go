package unifiedindex

import (
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	searchcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
)

var (
	htmlTagPattern        = regexp.MustCompile(`<[^>]+>`)
	urlPattern            = regexp.MustCompile(`https?://[^\s<>"')]+`)
	emailDomainPattern    = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@([A-Za-z0-9.\-]+\.[A-Za-z]{2,})`)
	currencyAmountPattern = regexp.MustCompile(`(?i)(?:€|\$|£|¥|EUR|USD|GBP|CNY|CHF|CAD|AUD)\s*([0-9]{1,6}(?:[.,][0-9]{3})*[.,][0-9]{2})\b|\b([0-9]{1,6}(?:[.,][0-9]{3})*[.,][0-9]{2})\s*(?:€|\$|£|EUR|USD|GBP)\b`)
	nonWordCleanupPattern = regexp.MustCompile(`[^\p{L}\p{N}]+`)
	ignoredGenericDomains = map[string]struct{}{
		"gmail.com": {}, "googlemail.com": {}, "icloud.com": {}, "me.com": {},
		"outlook.com": {}, "hotmail.com": {}, "yahoo.com": {}, "proton.me": {},
		"protonmail.com": {}, "w3.org": {}, "schema.org": {}, "googleapis.com": {},
		"googleusercontent.com": {}, "gstatic.com": {}, "fonts.googleapis.com": {},
		"googletagmanager.com": {}, "doubleclick.net": {}, "cloudfront.net": {},
		"amazonaws.com": {}, "gravatar.com": {}, "apple.com": {},
	}
	secondLevelPublicSuffixes = map[string]struct{}{
		"co.uk": {}, "org.uk": {}, "ac.uk": {}, "gov.uk": {},
		"com.au": {}, "co.jp": {}, "co.nz": {}, "com.br": {},
		"com.cn": {}, "com.hk": {}, "com.sg": {}, "co.kr": {},
	}
)

// CleanIndexedPlainText strips residual HTML tags, collapses whitespace, and
// caps length by rune count so FTS5 and snippets stay clean and bounded.
func CleanIndexedPlainText(raw string, maxRunes int) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.Contains(trimmed, "<") && strings.Contains(trimmed, ">") {
		trimmed = htmlTagPattern.ReplaceAllString(trimmed, " ")
	}
	var b strings.Builder
	b.Grow(min(len(trimmed), maxRunes*2))
	lastSpace := true
	runeCount := 0
	for _, r := range trimmed {
		if unicode.IsSpace(r) || r == '\u00a0' {
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
				runeCount++
				if runeCount >= maxRunes {
					break
				}
			}
			continue
		}
		if r < 32 || r == '\ufffc' {
			continue
		}
		b.WriteRune(r)
		lastSpace = false
		runeCount++
		if runeCount >= maxRunes {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

// ExpandEastAsianBigrams appends CJK character bigrams when Han, Kana, or
// Hangul runes are present so SQLite unicode61 tokenizes East Asian substrings.
func ExpandEastAsianBigrams(text string) string {
	var run []rune
	var bigrams []string
	flush := func() {
		if len(run) >= 2 {
			for i := 0; i < len(run)-1; i++ {
				bigrams = append(bigrams, string(run[i:i+2]))
			}
		}
		run = run[:0]
	}
	for _, r := range text {
		if isEastAsianScriptRune(r) {
			run = append(run, r)
			continue
		}
		flush()
	}
	flush()
	if len(bigrams) == 0 {
		return text
	}
	return text + " " + strings.Join(bigrams, " ")
}

func isEastAsianScriptRune(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r)
}

func TruncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 || len(s) <= maxRunes {
		return s
	}
	count := 0
	for i := range s {
		if count == maxRunes {
			return s[:i]
		}
		count++
	}
	return s
}

// ExtractRegistrableDomains extracts deduplicated registrable domains from
// sender addresses and URLs while skipping generic webmail and tracking hosts.
func ExtractRegistrableDomains(text string) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	seen := make(map[string]struct{}, 8)
	var out []string
	addHost := func(host string) {
		dom := NormalizeRegistrableDomain(host)
		if dom == "" {
			return
		}
		if _, skip := ignoredGenericDomains[dom]; skip {
			return
		}
		if _, dup := seen[dom]; dup {
			return
		}
		seen[dom] = struct{}{}
		out = append(out, dom)
	}
	for _, match := range emailDomainPattern.FindAllStringSubmatch(text, 16) {
		addHost(match[1])
	}
	for _, rawURL := range urlPattern.FindAllString(text, 16) {
		if parsed, err := url.Parse(rawURL); err == nil {
			addHost(parsed.Hostname())
		}
	}
	return out
}

func NormalizeRegistrableDomain(host string) string {
	h := strings.ToLower(strings.Trim(strings.TrimSpace(host), "."))
	if h == "" || strings.ContainsAny(h, " /:@") {
		return ""
	}
	parts := strings.Split(h, ".")
	if len(parts) < 2 {
		return ""
	}
	lastTwo := parts[len(parts)-2] + "." + parts[len(parts)-1]
	if _, multi := secondLevelPublicSuffixes[lastTwo]; multi && len(parts) >= 3 {
		return parts[len(parts)-3] + "." + lastTwo
	}
	return lastTwo
}

// ExtractMonetaryAmountMinorUnits parses explicit currency-annotated amounts
// into positive minor units (cents) for exact mechanical joins.
func ExtractMonetaryAmountMinorUnits(text string) []int64 {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	matches := currencyAmountPattern.FindAllStringSubmatch(text, 12)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(matches))
	var out []int64
	for _, m := range matches {
		raw := m[1]
		if raw == "" {
			raw = m[2]
		}
		minor, ok := ParseAmountStringToMinorUnits(raw)
		if !ok || minor <= 0 {
			continue
		}
		if _, dup := seen[minor]; dup {
			continue
		}
		seen[minor] = struct{}{}
		out = append(out, minor)
	}
	return out
}

func ParseAmountStringToMinorUnits(raw string) (int64, bool) {
	s := strings.TrimSpace(raw)
	if len(s) < 4 {
		return 0, false
	}
	sepIdx := len(s) - 3
	sep := s[sepIdx]
	if sep != '.' && sep != ',' {
		return 0, false
	}
	intPart := strings.ReplaceAll(strings.ReplaceAll(s[:sepIdx], ",", ""), ".", "")
	fracPart := s[sepIdx+1:]
	whole, err1 := strconv.ParseInt(intPart, 10, 64)
	frac, err2 := strconv.ParseInt(fracPart, 10, 64)
	if err1 != nil || err2 != nil || whole < 0 || frac < 0 || frac > 99 {
		return 0, false
	}
	return whole*100 + frac, true
}

func ParseDecimalStringToMinorUnits(raw string) (int64, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, false
	}
	val, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(val) || math.IsInf(val, 0) {
		return 0, false
	}
	return int64(math.Round(val * 100)), true
}

func TokenizeSearchWords(text string) []string {
	cleaned := nonWordCleanupPattern.ReplaceAllString(strings.ToLower(text), " ")
	fields := strings.Fields(cleaned)
	seen := make(map[string]struct{}, len(fields))
	var tokens []string
	for _, f := range fields {
		if len(f) < 2 {
			continue
		}
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		tokens = append(tokens, f)
	}
	return tokens
}

// BuildHighlightedMatchFragments slices a plain-text excerpt around the first
// matching query token and marks matching spans for canonical rendering.
func BuildHighlightedMatchFragments(text string, queryTokens []string, maxRunes int) []*searchcontract.SearchMatchTextFragment {
	clean := strings.TrimSpace(text)
	if clean == "" {
		return nil
	}
	excerpt := extractExcerptAroundTokens(clean, queryTokens, maxRunes)
	if len(queryTokens) == 0 {
		return []*searchcontract.SearchMatchTextFragment{{
			SearchMatchTextFragmentContent: excerpt,
		}}
	}
	lower := strings.ToLower(excerpt)
	if len(lower) != len(excerpt) {
		return []*searchcontract.SearchMatchTextFragment{{
			SearchMatchTextFragmentContent: excerpt,
		}}
	}
	var fragments []*searchcontract.SearchMatchTextFragment
	cursor := 0
	for cursor < len(excerpt) {
		bestPos := -1
		bestLen := 0
		for _, tok := range queryTokens {
			if tok == "" {
				continue
			}
			absPos := findNextSearchTokenMatch(lower, cursor, tok)
			if absPos >= 0 {
				if bestPos == -1 || absPos < bestPos || (absPos == bestPos && len(tok) > bestLen) {
					bestPos = absPos
					bestLen = len(tok)
				}
			}
		}
		if bestPos == -1 {
			fragments = append(fragments, &searchcontract.SearchMatchTextFragment{
				SearchMatchTextFragmentContent: excerpt[cursor:],
			})
			break
		}
		if bestPos > cursor {
			fragments = append(fragments, &searchcontract.SearchMatchTextFragment{
				SearchMatchTextFragmentContent: excerpt[cursor:bestPos],
			})
		}
		end := min(bestPos+bestLen, len(excerpt))
		fragments = append(fragments, &searchcontract.SearchMatchTextFragment{
			SearchMatchTextFragmentContent:            excerpt[bestPos:end],
			SearchMatchTextFragmentMatchesSearchQuery: true,
		})
		cursor = end
	}
	return fragments
}

func extractExcerptAroundTokens(text string, queryTokens []string, maxRunes int) string {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	lower := strings.ToLower(text)
	firstByteIdx := -1
	if len(lower) == len(text) {
		for _, tok := range queryTokens {
			if idx := findNextSearchTokenMatch(lower, 0, tok); idx >= 0 && (firstByteIdx == -1 || idx < firstByteIdx) {
				firstByteIdx = idx
			}
		}
	}
	if firstByteIdx <= 0 {
		return string(runes[:maxRunes]) + "…"
	}
	matchRuneIdx := len([]rune(text[:firstByteIdx]))
	start := max(0, matchRuneIdx-maxRunes/3)
	end := min(len(runes), start+maxRunes)
	prefix := ""
	if start > 0 {
		prefix = "…"
	}
	suffix := ""
	if end < len(runes) {
		suffix = "…"
	}
	return prefix + string(runes[start:end]) + suffix
}

func findNextSearchTokenMatch(lower string, startOffset int, token string) int {
	if token == "" || startOffset >= len(lower) {
		return -1
	}
	containsEastAsian := false
	tokenRunes := 0
	for _, r := range token {
		tokenRunes++
		if isEastAsianScriptRune(r) {
			containsEastAsian = true
		}
	}
	if containsEastAsian {
		if rel := strings.Index(lower[startOffset:], token); rel >= 0 {
			return startOffset + rel
		}
		return -1
	}
	requireWordEnd := tokenRunes <= 3
	searchFrom := startOffset
	for searchFrom < len(lower) {
		rel := strings.Index(lower[searchFrom:], token)
		if rel < 0 {
			return -1
		}
		pos := searchFrom + rel
		end := pos + len(token)
		startOK := pos == 0
		if !startOK {
			prevRune, _ := utf8.DecodeLastRuneInString(lower[:pos])
			startOK = !isWordHighlightRune(prevRune)
		}
		endOK := !requireWordEnd || end == len(lower)
		if !endOK {
			nextRune, _ := utf8.DecodeRuneInString(lower[end:])
			endOK = !isWordHighlightRune(nextRune)
		}
		if startOK && endOK {
			return pos
		}
		_, step := utf8.DecodeRuneInString(lower[pos:])
		if step <= 0 {
			step = 1
		}
		searchFrom = pos + step
	}
	return -1
}

func isWordHighlightRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
