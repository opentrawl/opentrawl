// Package conformance provides reusable test helpers for crawler command
// output.
package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

var (
	snakeCaseKeyDumpPattern = regexp.MustCompile(`^\s*([a-z][a-z0-9]*(?:_[a-z0-9]+)+)\s*:\s+\S`)
	checkLinePattern        = regexp.MustCompile(`^\s*[a-z][a-z0-9_]*\s*:\s*(ok|empty|missing|fail|stale|error)(?:\s|$)`)
	keyValueDumpPattern     = regexp.MustCompile(`\b([A-Za-z][A-Za-z0-9_]{0,40})=("[^"]*"|\S+)`)
	base64RunPattern        = regexp.MustCompile(`\b[A-Za-z0-9+/_-]{41,}={0,2}\b`)
	enumPattern             = regexp.MustCompile(`\b(type|status)_[0-9]+\b`)
	appleConstantPattern    = regexp.MustCompile(`\b(?:MK|PH|CL|NS|UI)[A-Z]*(?:[A-Z][a-z0-9]+){2,}\b`)
	nonLowercaseMePattern   = regexp.MustCompile(`\b(ME|Me|mE)\b`)
)

type searchEnvelope struct {
	Results []json.RawMessage `json:"results"`
}

type searchResult struct {
	Ref                   *string          `json:"ref"`
	Time                  *string          `json:"time"`
	Who                   *string          `json:"who"`
	Where                 *string          `json:"where"`
	Snippet               *string          `json:"snippet"`
	AnchorID              *string          `json:"anchor_id"`
	Summary               *searchSummary   `json:"summary"`
	ArchiveContext        []archiveContext `json:"archive_context"`
	Evidence              []searchEvidence `json:"evidence"`
	SnippetFrontTruncated *bool            `json:"snippet_front_truncated"`
	FrontTruncated        *bool            `json:"front_truncated"`
	TruncatedFront        *bool            `json:"truncated_front"`
}

type searchSummary struct {
	Title    *string `json:"title"`
	Subtitle *string `json:"subtitle"`
}
type archiveContext struct {
	Kind  *string `json:"kind"`
	Label *string `json:"label"`
}
type searchEvidence struct {
	Label    *string     `json:"label"`
	Text     *searchRuns `json:"text"`
	Field    *searchRuns `json:"field"`
	Media    *searchRuns `json:"media"`
	Relation *searchRuns `json:"relation"`
}
type searchRuns struct {
	Runs        []searchRun `json:"runs"`
	Value       []searchRun `json:"value"`
	Description []searchRun `json:"description"`
	Target      []searchRun `json:"target"`
}
type searchRun struct {
	Text    *string `json:"text"`
	Matched bool    `json:"matched"`
}

func AssertSearchEnvelope(t *testing.T, envelope []byte) {
	t.Helper()
	for _, failure := range CheckSearchEnvelope(envelope) {
		t.Error(failure)
	}
}

func CheckHumanOutput(humanOutput string) []string {
	var failures []string
	lines := strings.Split(humanOutput, "\n")
	for i, line := range lines {
		lineNo := i + 1
		if strings.ContainsRune(line, '\uFFFD') {
			failures = append(failures, fmt.Sprintf("line %d contains U+FFFD replacement text", lineNo))
		}
		if match := snakeCaseKeyDumpPattern.FindStringSubmatch(line); match != nil && !isTableLine(line) && !checkLinePattern.MatchString(line) {
			failures = append(failures, fmt.Sprintf("line %d looks like a snake_case key dump: %s", lineNo, match[1]))
		}
		if match := keyValueDumpPattern.FindStringSubmatch(line); match != nil {
			failures = append(failures, fmt.Sprintf("line %d looks like a key=value dump: %s", lineNo, match[1]))
		}
		if match := base64RunPattern.FindString(line); looksLikeBase64(match) {
			failures = append(failures, fmt.Sprintf("line %d contains a base64-like run over 40 characters", lineNo))
		}
		if match := enumPattern.FindString(line); match != "" {
			failures = append(failures, fmt.Sprintf("line %d contains an unmapped enum value: %s", lineNo, match))
		}
		if match := appleConstantPattern.FindString(line); match != "" {
			failures = append(failures, fmt.Sprintf("line %d contains a raw Apple framework constant: %s", lineNo, match))
		}
		if match := nonLowercaseMePattern.FindString(line); match != "" {
			failures = append(failures, fmt.Sprintf("line %d renders %q; use lowercase me", lineNo, match))
		}
	}
	return failures
}

func CheckSearchEnvelope(envelope []byte) []string {
	var parsed searchEnvelope
	dec := json.NewDecoder(bytes.NewReader(envelope))
	if err := dec.Decode(&parsed); err != nil {
		return []string{fmt.Sprintf("search envelope is not valid JSON: %v", err)}
	}
	if parsed.Results == nil {
		return []string{"search envelope is missing results array"}
	}
	var failures []string
	for i, raw := range parsed.Results {
		var result searchResult
		if err := json.Unmarshal(raw, &result); err != nil {
			failures = append(failures, fmt.Sprintf("search result %d is not an object: %v", i, err))
			continue
		}
		failures = append(failures, checkSearchResult(i, result)...)
	}
	return failures
}

func checkSearchResult(index int, result searchResult) []string {
	var failures []string
	if result.Time == nil || strings.TrimSpace(*result.Time) == "" {
		failures = append(failures, fmt.Sprintf("search result %d has no time", index))
	} else if _, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(*result.Time)); err != nil {
		failures = append(failures, fmt.Sprintf("search result %d time is not RFC3339: %q", index, *result.Time))
	}
	if result.Ref == nil || strings.TrimSpace(*result.Ref) == "" {
		failures = append(failures, fmt.Sprintf("search result %d has no ref", index))
	} else if !sourcePrefixedRef(*result.Ref) {
		failures = append(failures, fmt.Sprintf("search result %d ref is not source-prefixed: %q", index, *result.Ref))
	}
	for _, field := range []struct {
		name  string
		value *string
	}{
		{name: "ref", value: result.Ref},
		{name: "anchor_id", value: result.AnchorID},
		{name: "who", value: result.Who},
		{name: "where", value: result.Where},
		{name: "snippet", value: result.Snippet},
	} {
		if field.value != nil {
			failures = append(failures, checkSearchText(index, field.name, *field.value)...)
		}
	}
	if result.Summary == nil || result.Summary.Title == nil || strings.TrimSpace(*result.Summary.Title) == "" {
		failures = append(failures, fmt.Sprintf("search result %d has no summary title", index))
	}
	if len(result.ArchiveContext) == 0 {
		failures = append(failures, fmt.Sprintf("search result %d has no archive context", index))
	}
	for _, value := range result.ArchiveContext {
		if value.Kind == nil || !validArchiveContextKind(*value.Kind) {
			failures = append(failures, fmt.Sprintf("search result %d has invalid archive context kind", index))
		}
		if value.Label == nil || strings.TrimSpace(*value.Label) == "" {
			failures = append(failures, fmt.Sprintf("search result %d has empty archive context label", index))
		} else {
			failures = append(failures, checkSearchText(index, "archive_context", *value.Label)...)
		}
	}
	if len(result.Evidence) == 0 {
		failures = append(failures, fmt.Sprintf("search result %d has no evidence", index))
	}
	if result.SnippetFrontTruncated != nil && *result.SnippetFrontTruncated && (result.Snippet == nil || !strings.HasPrefix(strings.TrimSpace(*result.Snippet), "…")) {
		failures = append(failures, fmt.Sprintf("search result %d snippet is front-truncated but has no leading marker", index))
	}
	return failures
}

func checkSearchText(index int, field, value string) []string {
	var failures []string
	if strings.ContainsRune(value, '\uFFFD') {
		failures = append(failures, fmt.Sprintf("search result %d %s contains U+FFFD replacement text", index, field))
	}
	if match := base64RunPattern.FindString(value); looksLikeBase64(match) {
		failures = append(failures, fmt.Sprintf("search result %d %s contains a base64-like run over 40 characters", index, field))
	}
	if match := enumPattern.FindString(value); match != "" {
		failures = append(failures, fmt.Sprintf("search result %d %s contains an unmapped enum value: %s", index, field, match))
	}
	if match := appleConstantPattern.FindString(value); match != "" {
		failures = append(failures, fmt.Sprintf("search result %d %s contains a raw Apple framework constant: %s", index, field, match))
	}
	if field == "who" || field == "where" {
		if match := nonLowercaseMePattern.FindString(value); match != "" {
			failures = append(failures, fmt.Sprintf("search result %d %s renders %q; use lowercase me", index, field, match))
		}
	}
	return failures
}

func validArchiveContextKind(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func isTableLine(line string) bool {
	return strings.Contains(line, "\t") || strings.Contains(line, "|")
}

func sourcePrefixedRef(ref string) bool {
	prefix, rest, ok := strings.Cut(strings.TrimSpace(ref), ":")
	if !ok || strings.TrimSpace(rest) == "" {
		return false
	}
	if prefix == "" {
		return false
	}
	for i, r := range prefix {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

// looksLikeBase64 filters candidate runs: 40+ chars of real base64
// binary data virtually always carries + or = padding, while long
// alphanumeric path segments (temp dirs, test names, content hashes
// in URLs) carry neither. Requiring one kills those false positives.
func looksLikeBase64(match string) bool {
	if match == "" {
		return false
	}
	return strings.ContainsAny(match, "+=")
}
