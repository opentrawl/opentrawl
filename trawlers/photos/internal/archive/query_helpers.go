package archive

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/flags"
)

func photoSearchMatch(kind, matchID, title, body string) (string, []SearchMatch) {
	anchorID := strings.TrimSpace(kind)
	if anchorID == "" || anchorID == "asset" {
		anchorID = "asset-details"
	}
	if kind == "metadata" {
		anchorID = metadataAnchorID(matchID)
	}
	for _, value := range []string{title, body} {
		if runs := markedSearchRuns(value); len(runs) > 0 {
			return anchorID, []SearchMatch{{Field: kind, Runs: runs}}
		}
	}
	return anchorID, nil
}

func metadataAnchorID(id string) string {
	return "metadata." + base64.RawURLEncoding.EncodeToString([]byte(id))
}

func metadataIDForAnchor(anchorID string) (string, bool) {
	const prefix = "metadata."
	if !strings.HasPrefix(anchorID, prefix) {
		return "", false
	}
	id, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(anchorID, prefix))
	if err != nil || len(id) == 0 {
		return "", false
	}
	return string(id), true
}

func markedSearchRuns(value string) []SearchTextRun {
	const start, end = "\ue000", "\ue001"
	if !strings.Contains(value, start) {
		return nil
	}
	var runs []SearchTextRun
	for value != "" {
		startIndex := strings.Index(value, start)
		if startIndex < 0 {
			runs = appendSearchRun(runs, value, false)
			break
		}
		runs = appendSearchRun(runs, value[:startIndex], false)
		value = value[startIndex+len(start):]
		endIndex := strings.Index(value, end)
		if endIndex < 0 {
			return nil
		}
		runs = appendSearchRun(runs, value[:endIndex], true)
		value = value[endIndex+len(end):]
	}
	return runs
}

func appendSearchRun(runs []SearchTextRun, text string, matched bool) []SearchTextRun {
	if text == "" {
		return runs
	}
	if len(runs) > 0 && runs[len(runs)-1].Matched == matched {
		runs[len(runs)-1].Text += text
		return runs
	}
	return append(runs, SearchTextRun{Text: text, Matched: matched})
}

func markedSnippetMatchesAlbum(snippet, albumTitles string) bool {
	if strings.TrimSpace(albumTitles) == "" {
		return false
	}
	for _, run := range markedSearchRuns(snippet) {
		if !run.Matched {
			continue
		}
		for _, title := range strings.Split(albumTitles, "\n") {
			if strings.Contains(strings.ToLower(title), strings.ToLower(run.Text)) {
				return true
			}
		}
	}
	return false
}

func matchedAssetField(ctx context.Context, db *sql.DB, assetID, kind, snippet string) (string, error) {
	if kind == "summary" {
		rows, err := rows(ctx, db, `
select observation_type, value_text
from model_observation
where asset_id = ? and observation_type in (?, ?, ?, ?) and superseded_at is null
order by case observation_type when ? then 1 when ? then 2 when ? then 3 else 4 end, id
`, assetID, modelObservationCardSummary, modelObservationCardDescription, modelObservationCardOCR, modelObservationCardUncertainty, modelObservationCardSummary, modelObservationCardDescription, modelObservationCardOCR)
		if err != nil {
			return kind, err
		}
		for _, row := range rows {
			if markedSnippetMatchesText(snippet, rowString(row, "value_text")) {
				switch rowString(row, "observation_type") {
				case modelObservationCardDescription:
					return "description", nil
				case modelObservationCardOCR:
					return "ocr", nil
				case modelObservationCardUncertainty:
					return "uncertainty", nil
				}
			}
		}
	}
	if kind == "media" {
		rows, err := rows(ctx, db, `select original_filename from asset_resource where asset_id = ? order by id`, assetID)
		if err != nil {
			return kind, err
		}
		for _, row := range rows {
			if markedSnippetMatchesText(snippet, rowString(row, "original_filename")) {
				return "filenames", nil
			}
		}
	}
	return kind, nil
}

func markedSnippetMatchesText(snippet, value string) bool {
	for _, run := range markedSearchRuns(snippet) {
		if run.Matched && strings.Contains(strings.ToLower(value), strings.ToLower(run.Text)) {
			return true
		}
	}
	return false
}

func AssetRef(id string) string {
	return photoscrawlRef(id)
}

func photoscrawlRef(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return "photos:" + strings.Replace(id, ":", "/", 1)
}

func normalizeRef(ref string) string {
	ref = strings.TrimSpace(ref)
	for _, prefix := range []string{"photos:", "photoscrawl:"} {
		ref = strings.TrimPrefix(ref, prefix)
	}
	return strings.Replace(ref, "/", ":", 1)
}

func searchTimeBound(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := flags.Date(value)
	if err != nil {
		return "", fmt.Errorf("invalid time %q", value)
	}
	return parsed.Format(time.RFC3339), nil
}

// localRFC3339 is for machine events only (e.g. sync time) — those genuinely
// happened in this machine's timezone. Capture times use localCaptureTime.
func localRFC3339(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return parsed.Local().Format(time.RFC3339)
}

func searchSnippet(query, cardSummary, cardDescription, title, assetBody string) string {
	cardText := cleanSnippet(strings.Join([]string{cardSummary, cardDescription}, " "))
	if cardText != "" {
		return textFragment(query, cardText)
	}
	return textFragment(query, cleanSnippet(strings.Join([]string{title, assetBody}, " ")))
}

func cleanSnippet(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func textFragment(query, text string) string {
	const maxSnippet = 180
	text = cleanSnippet(text)
	if text == "" {
		return ""
	}
	lowerText := strings.ToLower(text)
	start := 0
	for _, term := range strings.Fields(strings.ToLower(query)) {
		term = strings.Trim(term, `"':,.;!?()[]{}<>`)
		if term == "" {
			continue
		}
		if idx := strings.Index(lowerText, term); idx >= 0 {
			start = idx - 60
			if start < 0 {
				start = 0
			}
			break
		}
	}
	if start > 0 {
		if nextSpace := strings.IndexByte(text[start:], ' '); nextSpace >= 0 {
			start += nextSpace + 1
		}
	}
	if len(text)-start <= maxSnippet {
		fragment := strings.TrimSpace(text[start:])
		if start > 0 {
			return "..." + fragment
		}
		return fragment
	}
	end := start + maxSnippet
	if end > len(text) {
		end = len(text)
	}
	if end < len(text) {
		if previousSpace := strings.LastIndexByte(text[start:end], ' '); previousSpace > 0 {
			end = start + previousSpace
		}
	}
	fragment := strings.TrimSpace(text[start:end])
	if start > 0 {
		fragment = "..." + fragment
	}
	if end < len(text) {
		fragment += "..."
	}
	return fragment
}

func oneRow(ctx context.Context, db *sql.DB, query string, args ...any) (map[string]any, error) {
	result, err := rows(ctx, db, query, args...)
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, sql.ErrNoRows
	}
	return result[0], nil
}

func rows(ctx context.Context, db *sql.DB, query string, args ...any) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(columns))
		for i, column := range columns {
			row[column] = normalizeSQLValue(values[i])
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func normalizeSQLValue(value any) any {
	switch typed := value.(type) {
	case []byte:
		return string(typed)
	default:
		return typed
	}
}

func ftsQuery(query string) string {
	terms := strings.Fields(query)
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		term = strings.ReplaceAll(term, `"`, `""`)
		quoted = append(quoted, `"`+term+`"`)
	}
	if len(quoted) == 0 {
		return `""`
	}
	// OR, not AND: cards are short, agents search with descriptive phrases,
	// and bm25 already ranks docs matching more terms higher. AND made
	// "Castellbell house village street" return nothing.
	return strings.Join(quoted, " OR ")
}
