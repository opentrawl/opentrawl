package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/store"
)

type SearchOptions struct {
	Query  string
	Limit  int
	After  string
	Before string
}

type SearchResult struct {
	Query        string      `json:"query"`
	Limit        int         `json:"-"`
	Results      []SearchHit `json:"results"`
	TotalMatches int         `json:"total_matches"`
	Truncated    bool        `json:"truncated"`
}

type SearchHit struct {
	Ref     string `json:"ref"`
	Time    string `json:"time"`
	Who     string `json:"who"`
	Where   string `json:"where"`
	Snippet string `json:"snippet"`

	ID           string `json:"-"`
	HitType      string `json:"-"`
	MediaType    string `json:"-"`
	CreationDate string `json:"-"`
	Title        string `json:"-"`
}

const searchWhoSQL = `coalesce((
  select group_concat(person_label, ', ')
  from (
    select distinct person_label
    from face_observation
    where asset_id = asset.id and trim(person_label) <> ''
    order by person_label
    limit 3
  )
), '')`

const searchWhereSQL = `coalesce((
  select value_text
  from model_observation
  where asset_id = asset.id
    and trim(value_text) <> ''
    and observation_type = '` + modelObservationCardPlacePhrase + `'
  order by id
  limit 1
), (
  select 'GPS ' || printf('%.4f', latitude) || ', ' || printf('%.4f', longitude) ||
         case when horizontal_accuracy is not null then ' +/-' || printf('%.0f', horizontal_accuracy) || 'm' else '' end
  from location_observation
  where asset_id = asset.id
  order by id
  limit 1
), '')`

const searchCardSummarySQL = `coalesce((
  select value_text
  from model_observation
  where asset_id = asset.id
    and observation_type = '` + modelObservationCardSummary + `'
    and trim(value_text) <> ''
  order by id
  limit 1
), '')`

const searchCardDescriptionSQL = `coalesce((
  select value_text
  from model_observation
  where asset_id = asset.id
    and observation_type = '` + modelObservationCardDescription + `'
    and trim(value_text) <> ''
  order by id
  limit 1
), '')`

func Search(ctx context.Context, paths Paths, opts SearchOptions) (SearchResult, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return SearchResult{}, errors.New("query is required")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	after, err := searchTimeBound(opts.After)
	if err != nil {
		return SearchResult{}, fmt.Errorf("after must be a date (2006-01-02) or RFC 3339 timestamp: %w", err)
	}
	before, err := searchTimeBound(opts.Before)
	if err != nil {
		return SearchResult{}, fmt.Errorf("before must be a date (2006-01-02) or RFC 3339 timestamp: %w", err)
	}
	db, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		return SearchResult{}, err
	}
	defer db.Close()

	fts := ftsQuery(query)
	totalMatches, err := ftsDistinctAssetCount(ctx, db.DB(), fts, after, before)
	if err != nil {
		return SearchResult{}, fmt.Errorf("count search matches: %w", err)
	}
	rows, err := db.DB().QueryContext(ctx, `
with asset_matches as (
  select asset.id, asset_fts.rank as hit_rank
  from asset_fts
  join asset on asset.id = asset_fts.id
  where asset_fts match ?
    and (? = '' or asset.creation_date >= ?)
    and (? = '' or asset.creation_date <= ?)
),
card_matches as (
  select asset.id, observation_fts.rank as hit_rank
  from observation_fts
  join model_observation card_summary on card_summary.id = observation_fts.id
    and card_summary.asset_id = observation_fts.asset_id
    and card_summary.observation_type = '`+modelObservationCardSummary+`'
  join asset on asset.id = observation_fts.asset_id
  where observation_fts match ?
    and (? = '' or asset.creation_date >= ?)
    and (? = '' or asset.creation_date <= ?)
),
matched_assets as (
  select id, min(hit_rank) as hit_rank
  from (
    select id, hit_rank from asset_matches
    union all
    select id, hit_rank from card_matches
  )
  group by id
)
select asset.id, asset.media_type, asset.creation_date,
       coalesce((select title from asset_fts where id = asset.id limit 1), '') as title,
       coalesce((select body from asset_fts where id = asset.id limit 1), '') as asset_body,
       `+searchWhoSQL+` as who,
       `+searchWhereSQL+` as where_label,
       `+searchCardSummarySQL+` as card_summary,
       `+searchCardDescriptionSQL+` as card_description
from matched_assets
join asset on asset.id = matched_assets.id
order by matched_assets.hit_rank, asset.creation_date desc, asset.id
limit ?
`, fts, after, after, before, before, fts, after, after, before, before, limit)
	if err != nil {
		return SearchResult{}, fmt.Errorf("search assets: %w", err)
	}
	defer rows.Close()

	result := SearchResult{
		Query:        query,
		Limit:        limit,
		Results:      []SearchHit{},
		TotalMatches: totalMatches,
		Truncated:    totalMatches > limit,
	}
	for rows.Next() {
		var hit SearchHit
		var assetBody, cardSummary, cardDescription string
		if err := rows.Scan(&hit.ID, &hit.MediaType, &hit.CreationDate, &hit.Title, &assetBody, &hit.Who, &hit.Where, &cardSummary, &cardDescription); err != nil {
			return SearchResult{}, err
		}
		hit.HitType = "asset"
		hit.Ref = assetRef(hit.ID)
		hit.Time = localRFC3339(hit.CreationDate)
		if !strings.HasPrefix(hit.Where, "GPS ") {
			hit.Where = cleanPlacePhrase(hit.Where)
		}
		hit.Snippet = searchSnippet(query, cardSummary, cardDescription, hit.Title, assetBody)
		result.Results = append(result.Results, hit)
	}
	if err := rows.Err(); err != nil {
		return SearchResult{}, err
	}
	return result, nil
}

func ftsDistinctAssetCount(ctx context.Context, db *sql.DB, fts, after, before string) (int, error) {
	var count int
	if err := db.QueryRowContext(ctx, `
with asset_matches as (
  select asset.id
  from asset_fts
  join asset on asset.id = asset_fts.id
  where asset_fts match ?
    and (? = '' or asset.creation_date >= ?)
    and (? = '' or asset.creation_date <= ?)
),
card_matches as (
  select asset.id
  from observation_fts
  join model_observation card_summary on card_summary.id = observation_fts.id
    and card_summary.asset_id = observation_fts.asset_id
    and card_summary.observation_type = '`+modelObservationCardSummary+`'
  join asset on asset.id = observation_fts.asset_id
  where observation_fts match ?
    and (? = '' or asset.creation_date >= ?)
    and (? = '' or asset.creation_date <= ?)
)
select count(*)
from (
  select id from asset_matches
  union
  select id from card_matches
)
`, fts, after, after, before, before, fts, after, after, before, before).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func Open(ctx context.Context, paths Paths, rowID string) (OpenResult, error) {
	rowID = normalizeRef(rowID)
	if rowID == "" {
		return OpenResult{}, errors.New("ref is required")
	}
	db, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		return OpenResult{}, err
	}
	defer db.Close()

	asset, err := oneRow(ctx, db.DB(), `
select id, media_type, creation_date, width, height, duration_seconds, favorite, hidden
from asset
where id = ?
`, rowID)
	if errors.Is(err, sql.ErrNoRows) {
		return OpenResult{}, fmt.Errorf("asset not found: %s", rowID)
	}
	if err != nil {
		return OpenResult{}, err
	}
	resources, err := rows(ctx, db.DB(), `
select resource_type, uti, original_filename, file_size, available_locally, needs_download
from asset_resource
where asset_id = ?
order by resource_type, original_filename
`, rowID)
	if err != nil {
		return OpenResult{}, err
	}
	locations, err := rows(ctx, db.DB(), `
select id, latitude, longitude, altitude, horizontal_accuracy, source, evidence_id
from location_observation
where asset_id = ?
`, rowID)
	if err != nil {
		return OpenResult{}, err
	}
	modelObservations, err := rows(ctx, db.DB(), `
select observation_type, value_text, value_json, model_id, prompt_version, evidence_id
from model_observation
where asset_id = ?
  and observation_type in ('`+modelObservationCardSummary+`', '`+modelObservationCardDescription+`', '`+modelObservationCardPlacePhrase+`', '`+modelObservationCardUncertainty+`')
order by case observation_type
  when '`+modelObservationCardSummary+`' then 1
  when '`+modelObservationCardDescription+`' then 2
  when '`+modelObservationCardPlacePhrase+`' then 3
  when '`+modelObservationCardUncertainty+`' then 4
  else 5
end, id
`, rowID)
	if err != nil {
		return OpenResult{}, err
	}
	evidence, err := evidenceRows(ctx, db.DB(), rowID)
	if err != nil {
		return OpenResult{}, err
	}
	return newOpenResult(asset, resources, locations, modelObservations, evidence), nil
}

func assetRef(id string) string {
	return photoscrawlRef(id)
}

func photoscrawlRef(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return "photoscrawl:" + strings.Replace(id, ":", "/", 1)
}

func normalizeRef(ref string) string {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "photoscrawl:")
	return strings.Replace(ref, "/", ":", 1)
}

func searchTimeBound(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC().Format(time.RFC3339), nil
		}
	}
	return "", fmt.Errorf("invalid time %q", value)
}

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

func cleanSnippet(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func searchSnippet(query, cardSummary, cardDescription, title, assetBody string) string {
	cardText := cleanSnippet(strings.Join([]string{cardSummary, cardDescription}, " "))
	if cardText != "" {
		return textFragment(query, cardText)
	}
	return textFragment(query, cleanSnippet(strings.Join([]string{title, assetBody}, " ")))
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

func Evidence(ctx context.Context, paths Paths, rowID string) (EvidenceResult, error) {
	rowID = normalizeRef(rowID)
	if rowID == "" {
		return EvidenceResult{}, errors.New("ref is required")
	}
	db, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		return EvidenceResult{}, err
	}
	defer db.Close()
	evidence, err := evidenceRows(ctx, db.DB(), rowID)
	if err != nil {
		return EvidenceResult{}, err
	}
	return EvidenceResult{Ref: photoscrawlRef(rowID), Evidence: openEvidenceRefs(evidence)}, nil
}

func evidenceRows(ctx context.Context, db *sql.DB, rowID string) ([]map[string]any, error) {
	return rows(ctx, db, `
select id, asset_id, evidence_kind, source
from evidence_ref
where asset_id = ? or id = ? or id in (
  select evidence_id from location_observation where id = ?
  union
  select evidence_id from metadata_observation where id = ?
  union
  select evidence_id from text_observation where id = ?
  union
  select evidence_id from face_observation where id = ?
  union
  select evidence_id from model_observation where id = ?
  union
  select evidence_id from edge where id = ?
)
order by evidence_kind, id
`, rowID, rowID, rowID, rowID, rowID, rowID, rowID, rowID)
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
	defer rows.Close()
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
	return strings.Join(quoted, " AND ")
}
